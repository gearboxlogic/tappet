package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gearboxlogic/capscope/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchFromConfigPreservesCompletePagedToolMetadata(t *testing.T) {
	t.Setenv("CAPSCOPE_GENERATOR_MODE", "serve")
	t.Setenv("CAPSCOPE_GENERATOR_COMMAND", "fixture")
	t.Setenv("CAPSCOPE_GENERATOR_HOST", "provider.invalid")
	t.Setenv("CAPSCOPE_GENERATOR_TOKEN", "fixture-token")
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"stdio-provider": {
			TransportType: config.MCPClientTypeStdio,
			Command:       "${CAPSCOPE_GENERATOR_COMMAND}",
			Args:          []string{"--${CAPSCOPE_GENERATOR_MODE}"},
			Env:           map[string]string{"TOKEN": "${CAPSCOPE_GENERATOR_TOKEN}"},
		},
		"streamable-provider": {
			TransportType: config.MCPClientTypeStreamable,
			URL:           "https://${CAPSCOPE_GENERATOR_HOST}/mcp",
			Headers:       map[string]string{"Authorization": "Bearer ${CAPSCOPE_GENERATOR_TOKEN}"},
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
	t.Setenv("CAPSCOPE_GENERATOR_COMMAND", os.Args[0])
	t.Setenv("CAPSCOPE_GENERATOR_CONFIGURED_VALUE", "configured-value")
	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"environment-fixture": {
			TransportType: config.MCPClientTypeStdio,
			Command:       "${CAPSCOPE_GENERATOR_COMMAND}",
			Args:          []string{"-test.run=^TestGeneratorStdioProvider$"},
			Env: map[string]string{
				"CAPSCOPE_GENERATOR_FIXTURE": "enabled",
				"CAPSCOPE_GENERATOR_VALUE":   "${CAPSCOPE_GENERATOR_CONFIGURED_VALUE}",
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
	provider.AddTool(
		mcp.NewTool("remote_tool", mcp.WithDescription("remote fixture")),
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
	t.Setenv("CAPSCOPE_GENERATOR_URL", server.URL)
	t.Setenv("CAPSCOPE_GENERATOR_AUTH", "configured-token")

	configPath := writeGeneratorConfig(t, Config{MCPServers: map[string]*config.MCPClientConfigV2{
		"streamable-fixture": {
			TransportType: config.MCPClientTypeStreamable,
			URL:           "${CAPSCOPE_GENERATOR_URL}",
			Headers:       map[string]string{"Authorization": "Bearer ${CAPSCOPE_GENERATOR_AUTH}"},
		},
	}})

	servers, err := fetchFromConfig(configPath)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Len(t, servers[0].Tools, 1)
	assert.Equal(t, "remote_tool", servers[0].Tools[0].Name)
	require.Equal(t, "Bearer configured-token", <-headers)
}

func TestGeneratorStdioProvider(t *testing.T) {
	if os.Getenv("CAPSCOPE_GENERATOR_FIXTURE") != "enabled" {
		return
	}
	fixture := mcpserver.NewMCPServer("generator-fixture", "test")
	fixture.AddTool(
		mcp.NewTool("environment_tool", mcp.WithDescription(os.Getenv("CAPSCOPE_GENERATOR_VALUE"))),
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
	mu          sync.Mutex
	pages       map[string]*mcp.ListToolsResult
	starts      int
	initializes int
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

func (c *fakeCatalogClient) ListToolsByPage(_ context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, ok := c.pages[string(request.Params.Cursor)]
	if !ok {
		return nil, errors.New("unexpected cursor")
	}
	copy := *result
	copy.Tools = append([]mcp.Tool(nil), result.Tools...)
	return &copy, nil
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
