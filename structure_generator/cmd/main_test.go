package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchFromConfigPreservesCompletePagedToolMetadata(t *testing.T) {
	t.Setenv("TAPPET_GENERATOR_MODE", "serve")
	t.Setenv("TAPPET_GENERATOR_COMMAND", "fixture")
	t.Setenv("TAPPET_GENERATOR_HOST", "provider.invalid")
	t.Setenv("TAPPET_GENERATOR_TOKEN", "fixture-token")
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"stdio-provider": {
			TransportType: config.MCPClientTypeStdio,
			Command:       "${TAPPET_GENERATOR_COMMAND}",
			Args:          []string{"--${TAPPET_GENERATOR_MODE}"},
			Env:           map[string]string{"TOKEN": "${TAPPET_GENERATOR_TOKEN}"},
		},
		"streamable-provider": {
			TransportType: config.MCPClientTypeStreamable,
			URL:           "https://${TAPPET_GENERATOR_HOST}/mcp",
			Headers:       map[string]string{"Authorization": "Bearer ${TAPPET_GENERATOR_TOKEN}"},
		},
	}})

	readOnly := true
	fixture := &fakeCatalogClient{pages: map[string]*mcp.ListToolsResult{
		"": {
			PaginatedResult: mcp.PaginatedResult{NextCursor: "page-2"},
			Tools: []mcp.Tool{{
				Name:        "first_tool",
				Description: "first page",
				InputSchema: mcp.ToolInputSchema{
					Type:       "object",
					Properties: map[string]interface{}{"value": map[string]interface{}{"type": "string"}},
					Required:   []string{"value"},
				},
				OutputSchema: mcp.ToolOutputSchema{
					Type:       "object",
					Properties: map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}},
				},
				Annotations: mcp.ToolAnnotation{Title: "First tool", ReadOnlyHint: &readOnly},
			}},
		},
		"page-2": {
			Tools: []mcp.Tool{{
				Name:        "second_tool",
				Description: "second page",
				InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
			}},
		},
	}}

	seen := make(map[string]*config.MCPClientConfigV2)
	servers, err := fetchFromConfigWithFactory(context.Background(), configPath, func(name string, cfg *config.MCPClientConfigV2) (catalogConnection, error) {
		seen[name] = cfg
		return catalogConnection{client: fixture, needStart: true}, nil
	})
	require.NoError(t, err)
	require.Len(t, servers, 2)
	assert.Equal(t, []string{"stdio-provider", "streamable-provider"}, []string{servers[0].ServerName, servers[1].ServerName})
	assert.Equal(t, "fixture", seen["stdio-provider"].Command)
	assert.Equal(t, "fixture-token", seen["stdio-provider"].Env["TOKEN"])
	assert.Equal(t, []string{"--serve"}, seen["stdio-provider"].Args)
	assert.Equal(t, "https://provider.invalid/mcp", seen["streamable-provider"].URL)
	assert.Equal(t, "Bearer fixture-token", seen["streamable-provider"].Headers["Authorization"])
	assert.Equal(t, 2, fixture.startCount())
	assert.Equal(t, 2, fixture.initializeCount())

	first := servers[0].Tools[0]
	assert.Equal(t, "First tool", first.Title)
	assert.Equal(t, "object", first.OutputSchema["type"])
	assert.Equal(t, true, first.Annotations["readOnlyHint"])
	require.Len(t, servers[0].Tools, 2)
	assert.Equal(t, "second_tool", servers[0].Tools[1].Name)
}

func TestFetchFromConfigRejectsRepeatedPaginationCursor(t *testing.T) {
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"cyclic-provider": {TransportType: config.MCPClientTypeStdio, Command: "fixture"},
	}})
	fixture := &fakeCatalogClient{
		pages: map[string]*mcp.ListToolsResult{
			"":         {PaginatedResult: mcp.PaginatedResult{NextCursor: "repeated"}},
			"repeated": {PaginatedResult: mcp.PaginatedResult{NextCursor: "repeated"}},
		},
		maxListCalls: 2,
	}

	servers, err := fetchFromConfigWithFactory(context.Background(), configPath, func(string, *config.MCPClientConfigV2) (catalogConnection, error) {
		return catalogConnection{client: fixture}, nil
	})

	assert.Nil(t, servers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `failed to list tools: repeated pagination cursor "repeated"`)
	assert.Equal(t, []string{"", "repeated"}, fixture.listRequests())
}

func TestFetchToolsFromServerEnforcesInventoryLimits(t *testing.T) {
	tool := func(name, description string) mcp.Tool {
		return mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
		}
	}
	tests := []struct {
		name         string
		pages        map[string]*mcp.ListToolsResult
		limits       inventoryLimits
		wantError    string
		wantRequests []string
	}{
		{
			name: "pages",
			pages: map[string]*mcp.ListToolsResult{
				"":       {PaginatedResult: mcp.PaginatedResult{NextCursor: "page-2"}},
				"page-2": {PaginatedResult: mcp.PaginatedResult{NextCursor: "page-3"}},
				"page-3": {},
			},
			limits:       inventoryLimits{pages: 2, tools: 10, pageBytes: 1_024, totalBytes: 4_096},
			wantError:    "inventory page limit exceeded: maximum 2 pages",
			wantRequests: []string{"", "page-2"},
		},
		{
			name: "tools",
			pages: map[string]*mcp.ListToolsResult{
				"": {Tools: []mcp.Tool{tool("one", ""), tool("two", "")}},
			},
			limits:       inventoryLimits{pages: 2, tools: 1, pageBytes: 1_024, totalBytes: 4_096},
			wantError:    "inventory tool limit exceeded: maximum 1 tools",
			wantRequests: []string{""},
		},
		{
			name: "page bytes",
			pages: map[string]*mcp.ListToolsResult{
				"": {Tools: []mcp.Tool{tool("one", "metadata exceeds a deliberately tiny page limit")}},
			},
			limits:       inventoryLimits{pages: 2, tools: 10, pageBytes: 2, totalBytes: 4_096},
			wantError:    "inventory page byte limit exceeded: maximum 2 encoded bytes",
			wantRequests: []string{""},
		},
		{
			name: "aggregate bytes",
			pages: map[string]*mcp.ListToolsResult{
				"": {Tools: []mcp.Tool{tool("one", "metadata exceeds a deliberately tiny aggregate limit")}},
			},
			limits:       inventoryLimits{pages: 2, tools: 10, pageBytes: 1_024, totalBytes: 2},
			wantError:    "inventory byte limit exceeded: maximum 2 encoded bytes",
			wantRequests: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &fakeCatalogClient{pages: tt.pages, maxListCalls: len(tt.wantRequests)}
			serverTools, err := fetchToolsFromServerWithLimits(
				context.Background(),
				"bounded-provider",
				&config.MCPClientConfigV2{},
				func(string, *config.MCPClientConfigV2) (catalogConnection, error) {
					return catalogConnection{client: fixture}, nil
				},
				tt.limits,
			)

			assert.Empty(t, serverTools)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
			assert.Equal(t, tt.wantRequests, fixture.listRequests())
		})
	}
}

func TestFetchFromConfigRejectsNullProviderConfig(t *testing.T) {
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"broken": nil,
	}})

	factoryCalled := false
	servers, err := fetchFromConfigWithFactory(context.Background(), configPath, func(string, *config.MCPClientConfigV2) (catalogConnection, error) {
		factoryCalled = true
		return catalogConnection{}, nil
	})

	assert.Nil(t, servers)
	require.EqualError(t, err, "invalid provider config for broken: configuration is null")
	assert.False(t, factoryCalled)
}

func TestFetchFromConfigRejectsPartialInventory(t *testing.T) {
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"available": {TransportType: config.MCPClientTypeStdio, Command: "fixture"},
		"broken":    {TransportType: config.MCPClientTypeStdio, Command: "fixture"},
	}})

	servers, err := fetchFromConfigWithFactory(context.Background(), configPath, func(name string, _ *config.MCPClientConfigV2) (catalogConnection, error) {
		if name == "broken" {
			return catalogConnection{}, errors.New("fixture unavailable")
		}
		return catalogConnection{client: &fakeCatalogClient{pages: map[string]*mcp.ListToolsResult{"": {}}}}, nil
	})

	assert.Nil(t, servers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch complete tool inventory from broken")
}

func TestFetchFromConfigPassesStdioEnvironment(t *testing.T) {
	t.Setenv("TAPPET_GENERATOR_COMMAND", os.Args[0])
	t.Setenv("TAPPET_GENERATOR_CONFIGURED_VALUE", "configured-value")
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"environment-fixture": {
			TransportType: config.MCPClientTypeStdio,
			Command:       "${TAPPET_GENERATOR_COMMAND}",
			Args:          []string{"-test.run=^TestGeneratorStdioProvider$"},
			Env: map[string]string{
				"TAPPET_GENERATOR_FIXTURE": "enabled",
				"TAPPET_GENERATOR_VALUE":   "${TAPPET_GENERATOR_CONFIGURED_VALUE}",
			},
		},
	}})

	servers, err := fetchFromConfig(configPath)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Len(t, servers[0].Tools, 1)
	assert.Equal(t, "configured-value", servers[0].Tools[0].Description)
}

func TestFetchFromConfigSupportsStreamableHTTPHeaders(t *testing.T) {
	provider := mcpserver.NewMCPServer("streamable-fixture", "test")
	remoteTool := mcp.NewToolWithRawSchema(
		"remote_tool",
		"remote fixture",
		json.RawMessage(`{
			"oneOf":[{"required":["query"]},{"required":["id"]}],
			"properties":{"query":{"type":"string"},"id":{"type":"integer"}},
			"additionalProperties":false
		}`),
	)
	remoteTool.RawOutputSchema = json.RawMessage(`{
		"type":"object",
		"patternProperties":{"^item_":{"type":"string"}}
	}`)
	provider.AddTool(
		remoteTool,
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	handler := mcpserver.NewStreamableHTTPServer(provider, mcpserver.WithStateLess(true))
	headers := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TAPPET_GENERATOR_URL", server.URL)
	t.Setenv("TAPPET_GENERATOR_AUTH", "configured-token")

	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"streamable-fixture": {
			TransportType: config.MCPClientTypeStreamable,
			URL:           "${TAPPET_GENERATOR_URL}",
			Headers:       map[string]string{"Authorization": "Bearer ${TAPPET_GENERATOR_AUTH}"},
		},
	}})

	servers, err := fetchFromConfig(configPath)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Len(t, servers[0].Tools, 1)
	assert.Equal(t, "remote_tool", servers[0].Tools[0].Name)
	assert.Contains(t, servers[0].Tools[0].InputSchema, "oneOf")
	assert.Equal(t, false, servers[0].Tools[0].InputSchema["additionalProperties"])
	assert.Contains(t, servers[0].Tools[0].OutputSchema, "patternProperties")
	require.Equal(t, "Bearer configured-token", <-headers)
}

func TestFetchFromConfigRejectsDuplicateOuterJSONRPCMembers(t *testing.T) {
	provider := mcpserver.NewMCPServer("duplicate-envelope-fixture", "test")
	handler := mcpserver.NewStreamableHTTPServer(provider, mcpserver.WithStateLess(true))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Method != "tools/list" {
			handler.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[]},"result":{"tools":[{"name":"hidden","inputSchema":{}}]}}`, request.ID)
	}))
	t.Cleanup(server.Close)

	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"duplicate-envelope": {
			TransportType: config.MCPClientTypeStreamable,
			URL:           server.URL,
		},
	}})

	servers, err := fetchFromConfig(configPath)

	assert.Nil(t, servers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate JSON object member "result"`)
}

func TestConvertToolPreservesSchemaNumbersAndTopLevelTitle(t *testing.T) {
	tool, err := convertTool(json.RawMessage(`{
		"name":"precise_tool",
		"title":"Top-level title",
		"inputSchema":{"type":"integer","maximum":9007199254740993},
		"outputSchema":{"type":"integer","minimum":-9007199254740993},
		"annotations":{"title":"Annotation title","readOnlyHint":true}
	}`))
	require.NoError(t, err)

	assert.Equal(t, "Top-level title", tool.Title)
	assert.Equal(t, json.Number("9007199254740993"), tool.InputSchema["maximum"])
	assert.Equal(t, json.Number("-9007199254740993"), tool.OutputSchema["minimum"])
	assert.Equal(t, "Annotation title", tool.Annotations["title"])
	assert.Equal(t, true, tool.Annotations["readOnlyHint"])

	encoded, err := json.Marshal(tool)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"maximum":9007199254740993`)
	assert.Contains(t, string(encoded), `"minimum":-9007199254740993`)
}

func TestConvertToolRejectsMissingInputSchema(t *testing.T) {
	for _, testCase := range []struct {
		name string
		tool string
	}{
		{name: "missing", tool: `{"name":"broken_tool"}`},
		{name: "null", tool: `{"name":"broken_tool","inputSchema":null}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := convertTool(json.RawMessage(testCase.tool))
			require.Error(t, err)
			assert.Contains(t, err.Error(), `tool "broken_tool" is missing required object inputSchema`)
		})
	}
}

func TestConvertToolRejectsDuplicateJSONMembers(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		tool      string
		duplicate string
	}{
		{
			name:      "top-level name",
			tool:      `{"name":"first","name":"second","inputSchema":{}}`,
			duplicate: "name",
		},
		{
			name:      "nested schema keyword",
			tool:      `{"name":"bounded","inputSchema":{"maximum":1,"maximum":2}}`,
			duplicate: "maximum",
		},
		{
			name:      "escape-equivalent schema keyword",
			tool:      `{"name":"bounded","inputSchema":{"maximum":1,"maxim\u0075m":2}}`,
			duplicate: "maximum",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := convertTool(json.RawMessage(testCase.tool))
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("duplicate JSON object member %q", testCase.duplicate))
		})
	}
}

func TestDecodeServerToolsPreservesNumericSchemaConstraints(t *testing.T) {
	serverTools, err := decodeServerTools([]byte(`{
		"serverName":"precision-provider",
		"tools":[{
			"name":"precise",
			"inputSchema":{},
			"outputSchema":{"type":"integer","minimum":9007199254740993}
		}]
	}`))
	require.NoError(t, err)
	require.Len(t, serverTools.Tools, 1)

	minimum, ok := serverTools.Tools[0].OutputSchema["minimum"].(json.Number)
	require.True(t, ok, "minimum decoded as %T", serverTools.Tools[0].OutputSchema["minimum"])
	assert.Equal(t, "9007199254740993", minimum.String())

	encoded, err := json.Marshal(serverTools)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"minimum":9007199254740993`)
}

func TestCatalogToolsPageRequiresToolsArray(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result string
	}{
		{name: "missing", result: `{}`},
		{name: "null", result: `{"tools":null}`},
		{name: "wrong type", result: `{"tools":{}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var page catalogToolsPage
			err := json.Unmarshal([]byte(testCase.result), &page)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `tools/list result`)
		})
	}
}

func TestCatalogToolsPageAcceptsEmptyToolsArray(t *testing.T) {
	var page catalogToolsPage
	require.NoError(t, json.Unmarshal([]byte(`{"tools":[],"nextCursor":"next"}`), &page))
	require.NotNil(t, page.Tools)
	assert.Empty(t, page.Tools)
	assert.Equal(t, mcp.Cursor("next"), page.NextCursor)
}

func TestCatalogToolsPageRejectsDuplicateJSONMembers(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		result    string
		duplicate string
	}{
		{
			name:      "tools",
			result:    `{"tools":[],"tools":[{"name":"hidden","inputSchema":{}}]}`,
			duplicate: "tools",
		},
		{
			name:      "next cursor",
			result:    `{"tools":[],"nextCursor":"first","nextCursor":"second"}`,
			duplicate: "nextCursor",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var page catalogToolsPage
			err := json.Unmarshal([]byte(testCase.result), &page)
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("duplicate JSON object member %q", testCase.duplicate))
		})
	}
}

func TestGeneratorStdioProvider(t *testing.T) {
	if os.Getenv("TAPPET_GENERATOR_FIXTURE") != "enabled" {
		return
	}
	fixture := mcpserver.NewMCPServer("generator-fixture", "test")
	fixture.AddTool(
		mcp.NewTool("environment_tool", mcp.WithDescription(os.Getenv("TAPPET_GENERATOR_VALUE"))),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	_ = mcpserver.ServeStdio(fixture)
	os.Exit(0)
}

func writeGeneratorConfig(t *testing.T, cfg Config) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

type fakeCatalogClient struct {
	mu           sync.Mutex
	pages        map[string]*mcp.ListToolsResult
	starts       int
	initializes  int
	requests     []string
	maxListCalls int
}

func (c *fakeCatalogClient) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	return nil
}

func (c *fakeCatalogClient) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initializes++
	return &mcp.InitializeResult{}, nil
}

func (c *fakeCatalogClient) ListToolsByPage(_ context.Context, request mcp.ListToolsRequest) (*catalogToolsPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cursor := string(request.Params.Cursor)
	c.requests = append(c.requests, cursor)
	if c.maxListCalls > 0 && len(c.requests) > c.maxListCalls {
		return nil, errors.New("too many list requests")
	}
	result, ok := c.pages[cursor]
	if !ok {
		return nil, errors.New("unexpected cursor")
	}
	tools := make([]json.RawMessage, 0, len(result.Tools))
	for _, tool := range result.Tools {
		encoded, err := json.Marshal(tool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, encoded)
	}
	return &catalogToolsPage{NextCursor: result.NextCursor, Tools: tools}, nil
}

func (*fakeCatalogClient) Close() error { return nil }

func (c *fakeCatalogClient) startCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts
}

func (c *fakeCatalogClient) initializeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initializes
}

func (c *fakeCatalogClient) listRequests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests...)
}
