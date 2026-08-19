package server

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gearboxlogic/capscope/internal/hierarchy"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCapScopeServerOutwardContract(t *testing.T) {
	h := loadServerTestHierarchy(t)
	registry := hierarchy.NewServerRegistry(nil)
	defer registry.Close()

	srv, err := NewCapScopeServer(ServerDependencies{
		Hierarchy: h,
		Registry:  registry,
		Name:      "CapScope",
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
	assert.Equal(t, "CapScope", initialized.ServerInfo.Name)
	assert.Equal(t, "0.1.0", initialized.ServerInfo.Version)

	listed, err := client.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 2)
	for i := range listed.Tools {
		names[i] = listed.Tools[i].Name
	}
	sort.Strings(names)
	assert.Equal(t, []string{"execute_tool", "get_tools_in_category"}, names)
}

func TestNewCapScopeServerCategoryOutputIsDeterministic(t *testing.T) {
	h := loadServerTestHierarchy(t)
	registry := hierarchy.NewServerRegistry(nil)
	defer registry.Close()
	srv, err := NewCapScopeServer(ServerDependencies{Hierarchy: h, Registry: registry, Name: "CapScope", Version: "test"})
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

func TestNewCapScopeServerRejectsMissingDependencies(t *testing.T) {
	_, err := NewCapScopeServer(ServerDependencies{})
	assert.EqualError(t, err, "hierarchy is required")

	h := loadServerTestHierarchy(t)
	_, err = NewCapScopeServer(ServerDependencies{Hierarchy: h})
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
