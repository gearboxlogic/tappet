package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gearboxlogic/tappet/internal/hierarchy"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTappetServerOutwardContract(t *testing.T) {
	h := loadServerTestHierarchy(t)
	registry := hierarchy.NewServerRegistry(nil)
	defer registry.Close()

	srv, err := NewTappetServer(ServerDependencies{
		Hierarchy: h,
		Registry:  registry,
		Name:      "Tappet",
		Version:   "0.1.0",
	})
	require.NoError(t, err)

	registered := srv.ListTools()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	assert.Equal(t, []string{"execute_tool", "get_tools_in_category"}, names)

	client, err := mcpclient.NewInProcessClient(srv)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Start(context.Background()))
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "contract-test"}
	initialized, err := client.Initialize(context.Background(), initRequest)
	require.NoError(t, err)
	assert.Equal(t, "Tappet", initialized.ServerInfo.Name)
	assert.Equal(t, "0.1.0", initialized.ServerInfo.Version)
	assert.NotNil(t, initialized.Capabilities.Tools)
	assert.Nil(t, initialized.Capabilities.Resources)
	assert.Nil(t, initialized.Capabilities.Sampling)
	assert.Nil(t, initialized.Capabilities.Elicitation)
	assert.Nil(t, initialized.Capabilities.Roots)

	listed, err := client.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 2)
	for i := range listed.Tools {
		names[i] = listed.Tools[i].Name
	}
	sort.Strings(names)
	assert.Equal(t, []string{"execute_tool", "get_tools_in_category"}, names)
}

func TestOutwardAuthCredentialIsExcludedFromRequestLogs(t *testing.T) {
	const token = "outward-auth-credential-bc1df7"

	previousLogOutput := log.Writer()
	previousLogFlags := log.Flags()
	previousLogPrefix := log.Prefix()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
		log.SetFlags(previousLogFlags)
		log.SetPrefix(previousLogPrefix)
	})

	called := false
	handler := chainMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		}),
		loggerMiddleware("tappet"),
		newAuthMiddleware([]string{token}),
	)
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.True(t, called)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.NotContains(t, logs.String(), token)
}

func TestModernMetadataValidationUsesInvalidParamsForMissingCapabilities(t *testing.T) {
	requestBody := []byte(`{"jsonrpc":"2.0","id":104,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	nextCalled := false
	handler := modernMetadataValidationMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(requestBody))

	handler.ServeHTTP(recorder, request)

	assert.False(t, nextCalled)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var response struct {
		ID    int `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, 104, response.ID)
	assert.Equal(t, mcp.INVALID_PARAMS, response.Error.Code)
}

func TestModernMetadataValidationPassesValidCapabilitiesUnchanged(t *testing.T) {
	requestBody := []byte(`{"jsonrpc":"2.0","id":105,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`)
	handler := modernMetadataValidationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, requestBody, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(requestBody))

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestProtocolValidationReaderRejectsMalformedModernFrameAndContinues(t *testing.T) {
	invalid := `{"jsonrpc":"2.0","id":104,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	valid := `{"jsonrpc":"2.0","id":105,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	var responses bytes.Buffer
	reader := newProtocolValidationReader(bytes.NewBufferString(invalid+"\n"+valid+"\n"), &responses)

	forwarded, err := io.ReadAll(reader)

	require.NoError(t, err)
	assert.Equal(t, valid+"\n", string(forwarded))
	var response struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(responses.Bytes()), &response))
	assert.Equal(t, mcp.INVALID_PARAMS, response.Error.Code)
}

func TestTappetStreamableHTTPNegotiatesModernStatelessProtocol(t *testing.T) {
	srv := newCharacterizationServer(t)
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStateLess(true)))
	t.Cleanup(httpServer.Close)

	httpTransport, err := transport.NewStreamableHTTP(httpServer.URL)
	require.NoError(t, err)
	client := mcpclient.NewClient(httpTransport)
	require.NoError(t, client.Start(t.Context()))
	t.Cleanup(func() { _ = client.Close() })

	request := mcp.InitializeRequest{}
	request.Params.ClientInfo = mcp.Implementation{Name: "modern-contract-test", Version: "1.0.0"}
	initialized, err := client.Initialize(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, mcp.ProtocolVersion20260728, client.ProtocolVersion())
	assert.Equal(t, mcp.ProtocolVersion20260728, initialized.ProtocolVersion)
	assert.Empty(t, httpTransport.GetSessionId())

	listed, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 2)
}

func TestTappetStreamableHTTPRetainsLegacyNegotiation(t *testing.T) {
	srv := newCharacterizationServer(t)
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStateLess(true)))
	t.Cleanup(httpServer.Close)

	httpTransport, err := transport.NewStreamableHTTP(httpServer.URL)
	require.NoError(t, err)
	client := mcpclient.NewClient(httpTransport, mcpclient.WithProtocolVersion(mcp.ProtocolVersion20251125))
	require.NoError(t, client.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	request := mcp.InitializeRequest{}
	request.Params.ClientInfo = mcp.Implementation{Name: "legacy-contract-test", Version: "1.0.0"}
	initialized, err := client.Initialize(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, mcp.ProtocolVersion20251125, client.ProtocolVersion())
	assert.Equal(t, mcp.ProtocolVersion20251125, initialized.ProtocolVersion)

	listed, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 2)
}

func TestTappetStdioNegotiatesModernAndLegacyProtocols(t *testing.T) {
	if os.Getenv("TAPPET_STDIO_MODERN_FIXTURE") == "1" {
		h, err := hierarchy.LoadHierarchy(os.Getenv("TAPPET_STDIO_HIERARCHY"))
		require.NoError(t, err)
		registry := hierarchy.NewServerRegistry(nil)
		defer registry.Close()
		srv, err := NewTappetServer(ServerDependencies{
			Hierarchy: h,
			Registry:  registry,
			Name:      "Tappet",
			Version:   "test",
		})
		require.NoError(t, err)
		require.NoError(t, mcpserver.ServeStdio(srv))
		return
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.json"), []byte(`{"overview":"stdio test root"}`), 0o644))
	tests := []struct {
		name            string
		clientOptions   []mcpclient.ClientOption
		protocolVersion string
	}{
		{
			name:            "modern",
			protocolVersion: mcp.ProtocolVersion20260728,
		},
		{
			name: "legacy",
			clientOptions: []mcpclient.ClientOption{
				mcpclient.WithProtocolVersion(mcp.ProtocolVersion20251125),
			},
			protocolVersion: mcp.ProtocolVersion20251125,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdioTransport := transport.NewStdioWithOptions(
				os.Args[0],
				[]string{
					"TAPPET_STDIO_MODERN_FIXTURE=1",
					"TAPPET_STDIO_HIERARCHY=" + dir,
				},
				[]string{"-test.run=^TestTappetStdioNegotiatesModernAndLegacyProtocols$"},
			)
			client := mcpclient.NewClient(stdioTransport, test.clientOptions...)
			require.NoError(t, client.Start(t.Context()))
			t.Cleanup(func() { _ = client.Close() })

			request := mcp.InitializeRequest{}
			request.Params.ClientInfo = mcp.Implementation{Name: test.name + "-stdio-contract-test", Version: "1.0.0"}
			initialized, err := client.Initialize(t.Context(), request)
			require.NoError(t, err)
			assert.Equal(t, test.protocolVersion, client.ProtocolVersion())
			assert.Equal(t, test.protocolVersion, initialized.ProtocolVersion)

			listed, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
			require.NoError(t, err)
			require.Len(t, listed.Tools, 2)
		})
	}
}

func TestNewTappetServerCategoryOutputIsDeterministic(t *testing.T) {
	h := loadServerTestHierarchy(t)
	registry := hierarchy.NewServerRegistry(nil)
	defer registry.Close()
	srv, err := NewTappetServer(ServerDependencies{Hierarchy: h, Registry: registry, Name: "Tappet", Version: "test"})
	require.NoError(t, err)

	handler := srv.ListTools()["get_tools_in_category"].Handler
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"path": ""}
	first, err := handler(context.Background(), request)
	require.NoError(t, err)
	second, err := handler(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestNewTappetServerRejectsMissingDependencies(t *testing.T) {
	_, err := NewTappetServer(ServerDependencies{})
	assert.EqualError(t, err, "hierarchy is required")

	h := loadServerTestHierarchy(t)
	_, err = NewTappetServer(ServerDependencies{Hierarchy: h})
	assert.EqualError(t, err, "server registry is required")
}

func loadServerTestHierarchy(t *testing.T) *hierarchy.Hierarchy {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root.json"), []byte(`{"overview":"server test root"}`), 0o644))
	h, err := hierarchy.LoadHierarchy(dir)
	require.NoError(t, err)
	return h
}

func newCharacterizationServer(t *testing.T) *mcpserver.MCPServer {
	t.Helper()
	h := loadServerTestHierarchy(t)
	registry := hierarchy.NewServerRegistry(nil)
	t.Cleanup(registry.Close)
	srv, err := NewTappetServer(ServerDependencies{
		Hierarchy: h,
		Registry:  registry,
		Name:      "Tappet",
		Version:   "test",
	})
	require.NoError(t, err)
	return srv
}
