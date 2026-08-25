package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gearboxlogic/tappet/internal/capability"
	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/gearboxlogic/tappet/internal/hierarchy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MiddlewareFunc func(http.Handler) http.Handler

func chainMiddleware(h http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for _, mw := range middlewares {
		h = mw(h)
	}
	return h
}

func newAuthMiddleware(tokens []string) MiddlewareFunc {
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(tokens) != 0 {
				token := r.Header.Get("Authorization")
				token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
				if token == "" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				if _, ok := tokenSet[token]; !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func loggerMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("<%s> Request [%s] %s", prefix, r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

func recoverMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("<%s> Recovered from panic: %v", prefix, err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ServerDependencies are the runtime objects shared by every Tappet transport.
type ServerDependencies struct {
	Hierarchy    *hierarchy.Hierarchy
	Capabilities *capability.Registry
	Registry     *hierarchy.ServerRegistry
	Name         string
	Version      string
	LogEnabled   bool
}

// NewTappetServer constructs the transport-independent outward MCP server.
func NewTappetServer(deps ServerDependencies) (*server.MCPServer, error) {
	if deps.Registry == nil {
		return nil, errors.New("server registry is required")
	}
	var backend brokerBackend
	if deps.Capabilities != nil {
		backend = capabilityBackend{capabilities: deps.Capabilities, providers: deps.Registry}
	} else if deps.Hierarchy != nil {
		backend = hierarchyBackend{hierarchy: deps.Hierarchy, providers: deps.Registry}
	} else {
		return nil, errors.New("capability registry or hierarchy is required")
	}

	serverOpts := []server.ServerOption{
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	}
	if deps.LogEnabled {
		serverOpts = append(serverOpts, server.WithLogging())
	}

	mcpServer := server.NewMCPServer(deps.Name, deps.Version, serverOpts...)
	description := "You have MCP tools hidden within categories. You MUST use get_tools_in_category to learn more about what available tools you have within these categories. Returns children categories, and tools at the specified path. Call initially with an empty string to get root categories."
	if overview := backend.RootOverview(); overview != "" {
		description += fmt.Sprintf("\n\n%s", overview)
	}

	getToolsInCategoryTool := mcp.Tool{
		Name:        "get_tools_in_category",
		Description: description,
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Category path using dot notation (e.g., 'coding_tools' or 'coding_tools.serena.search'). Use empty string or '/' for root.",
				},
			},
			Required: []string{"path"},
		},
	}
	mcpServer.AddTool(getToolsInCategoryTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path := ""
		if argsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if pathVal, ok := argsMap["path"].(string); ok {
				path = pathVal
			}
		}

		response, err := backend.Browse(path)
		if err != nil {
			return nil, err
		}
		jsonBytes, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(jsonBytes))}}, nil
	})

	executeToolTool := mcp.Tool{
		Name:        "execute_tool",
		Description: "Execute a tool by its full path. Automatically proxies the request to the appropriate MCP server.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"tool_path": map[string]interface{}{
					"type":        "string",
					"description": "Full tool path using dot notation (e.g., 'coding_tools.serena.search.search_symbol') or just tool name if unique",
				},
				"arguments": map[string]interface{}{
					"type":                 "object",
					"description":          "Arguments to pass to the tool",
					"additionalProperties": true,
				},
			},
			Required: []string{"tool_path", "arguments"},
		},
	}
	mcpServer.AddTool(executeToolTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolPath := ""
		arguments := make(map[string]interface{})
		if argsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if pathVal, ok := argsMap["tool_path"].(string); ok {
				toolPath = pathVal
			}
			if argsVal, ok := argsMap["arguments"].(map[string]interface{}); ok {
				arguments = argsVal
			}
		}
		if toolPath == "" {
			return nil, errors.New("tool_path is required")
		}
		return backend.Execute(ctx, toolPath, arguments)
	})

	return mcpServer, nil
}

type serverRuntime struct {
	providers    *hierarchy.ServerRegistry
	capabilities *capability.Registry
}

func (r *serverRuntime) Close() {
	if r.providers != nil {
		r.providers.Close()
	}
	if r.capabilities != nil {
		r.capabilities.Close()
	}
}

func loadServer(cfg *config.Config) (*server.MCPServer, *serverRuntime, error) {
	providerRegistry := hierarchy.NewServerRegistry(cfg.McpServers)
	runtime := &serverRuntime{providers: providerRegistry}
	deps := ServerDependencies{
		Registry:   providerRegistry,
		Name:       cfg.McpProxy.Name,
		Version:    cfg.McpProxy.Version,
		LogEnabled: cfg.McpProxy.Options != nil && cfg.McpProxy.Options.LogEnabled.OrElse(false),
	}
	if cfg.McpProxy.CapabilityPath != "" {
		log.Printf("Loading capability packages from %s", cfg.McpProxy.CapabilityPath)
		store, err := capability.NewSnapshotStore(capability.DefaultStoreLimits())
		if err != nil {
			runtime.Close()
			return nil, nil, fmt.Errorf("create capability snapshot store: %w", err)
		}
		loader, err := capability.NewLoader(cfg.McpProxy.CapabilityPath, store)
		if err != nil {
			runtime.Close()
			return nil, nil, fmt.Errorf("create capability loader: %w", err)
		}
		records, err := loader.LoadAll()
		if err != nil {
			runtime.Close()
			return nil, nil, fmt.Errorf("load capability packages: %w", err)
		}
		capabilityRegistry, err := capability.NewRegistry(records...)
		if err != nil {
			runtime.Close()
			return nil, nil, fmt.Errorf("create capability registry: %w", err)
		}
		runtime.capabilities = capabilityRegistry
		deps.Capabilities = capabilityRegistry
	} else {
		log.Printf("Loading compatibility hierarchy from %s", cfg.McpProxy.HierarchyPath)
		h, err := hierarchy.LoadHierarchy(cfg.McpProxy.HierarchyPath)
		if err != nil {
			runtime.Close()
			return nil, nil, fmt.Errorf("failed to load hierarchy: %w", err)
		}
		deps.Hierarchy = h
	}
	mcpServer, err := NewTappetServer(deps)
	if err != nil {
		runtime.Close()
		return nil, nil, err
	}
	return mcpServer, runtime, nil
}

// StartStdioServer starts the stdio server with the given configuration
func StartStdioServer(cfg *config.Config) error {
	mcpServer, runtime, err := loadServer(cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()
	log.Printf("Starting Tappet (stdio server)")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	stdout := &synchronizedWriter{writer: os.Stdout}
	stdin := newProtocolValidationReader(os.Stdin, stdout)
	return server.NewStdioServer(mcpServer).Listen(ctx, stdin, stdout)
}

// StartHTTPServer starts the HTTP server with the given configuration
func StartHTTPServer(cfg *config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpServer, runtime, err := loadServer(cfg)
	if err != nil {
		return err
	}
	defer runtime.Close()

	// Set up HTTP handler (SSE or Streamable)
	var handler http.Handler
	switch cfg.McpProxy.Type {
	case config.MCPServerTypeSSE:
		handler = server.NewSSEServer(
			mcpServer,
			server.WithStaticBasePath(""),
			server.WithBaseURL(cfg.McpProxy.BaseURL),
		)
	case config.MCPServerTypeStreamable:
		handler = server.NewStreamableHTTPServer(
			mcpServer,
			server.WithStateLess(true),
		)
	default:
		return fmt.Errorf("unknown server type: %s", cfg.McpProxy.Type)
	}
	handler = modernMetadataValidationMiddleware(handler)

	// Apply middleware
	middlewares := make([]MiddlewareFunc, 0)
	middlewares = append(middlewares, recoverMiddleware("tappet"))
	if cfg.McpProxy.Options != nil && cfg.McpProxy.Options.LogEnabled.OrElse(false) {
		middlewares = append(middlewares, loggerMiddleware("tappet"))
	}
	if cfg.McpProxy.Options != nil && len(cfg.McpProxy.Options.AuthTokens) > 0 {
		middlewares = append(middlewares, newAuthMiddleware(cfg.McpProxy.Options.AuthTokens))
	}
	handler = chainMiddleware(handler, middlewares...)

	// Start HTTP server
	httpMux := http.NewServeMux()
	httpMux.Handle("/", handler)

	httpServer := &http.Server{
		Addr:    cfg.McpProxy.Addr,
		Handler: httpMux,
	}

	go func() {
		log.Printf("Starting Tappet (%s server)", cfg.McpProxy.Type)
		log.Printf("%s server listening on %s", cfg.McpProxy.Type, cfg.McpProxy.Addr)
		hErr := httpServer.ListenAndServe()
		if hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", hErr)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()

	err = httpServer.Shutdown(shutdownCtx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
