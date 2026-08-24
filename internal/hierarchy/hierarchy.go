package hierarchy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gearboxlogic/tappet/internal/client"
	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// HierarchyNode represents a node in the tool hierarchy
// Can be a branch node (has children) or leaf node (has tools)
type HierarchyNode struct {
	Overview  string                     `json:"overview,omitempty"`
	Tools     map[string]*ToolDefinition `json:"tools,omitempty"`
	MCPServer *MCPServerRef              `json:"mcp_server,omitempty"`
}

// ToolDefinition represents a tool in the hierarchy
type ToolDefinition struct {
	Description string                 `json:"description,omitempty"`
	MapsTo      string                 `json:"maps_to,omitempty"`
	Server      string                 `json:"server,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// HierarchyNodeData is used for unmarshaling JSON with flexible tool types
type HierarchyNodeData struct {
	Overview  string                 `json:"overview,omitempty"`
	Tools     map[string]interface{} `json:"tools,omitempty"`
	MCPServer *MCPServerRef          `json:"mcp_server,omitempty"`
}

// MCPServerRef contains MCP server configuration
type MCPServerRef struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"` // "stdio", "sse", "streamable-http"
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	URL          string            `json:"url,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	ToolMappings map[string]string `json:"tool_mappings,omitempty"` // Maps hierarchy tool names to actual MCP tool names
}

// ToClientConfig converts MCPServerRef to MCPClientConfigV2
func (m *MCPServerRef) ToClientConfig() *config.MCPClientConfigV2 {
	cfg := &config.MCPClientConfigV2{
		Options: &config.OptionsV2{},
	}

	switch m.Type {
	case "stdio":
		cfg.TransportType = config.MCPClientTypeStdio
		cfg.Command = m.Command
		cfg.Args = m.Args
		cfg.Env = m.Env
	case "sse":
		cfg.TransportType = config.MCPClientTypeSSE
		cfg.URL = m.URL
		cfg.Headers = m.Headers
	case "streamable-http":
		cfg.TransportType = config.MCPClientTypeStreamable
		cfg.URL = m.URL
		cfg.Headers = m.Headers
	}

	return cfg
}

// Hierarchy manages the hierarchical tool structure
type Hierarchy struct {
	rootPath string
	nodes    map[string]*HierarchyNode
	mu       sync.RWMutex
}

// LoadHierarchy loads the hierarchy from a directory structure
func LoadHierarchy(hierarchyPath string) (*Hierarchy, error) {
	h := &Hierarchy{
		rootPath: hierarchyPath,
		nodes:    make(map[string]*HierarchyNode),
	}

	// Load root.json
	rootFile := filepath.Join(hierarchyPath, "root.json")
	rootNode, err := loadNode(rootFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load root node: %w", err)
	}
	h.nodes[""] = rootNode
	h.nodes["/"] = rootNode

	// Walk the directory structure and load all nodes
	err = filepath.Walk(hierarchyPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		if info.Name() == "root.json" {
			return nil // Already loaded
		}

		// Calculate the hierarchy path from the file path
		relPath, err := filepath.Rel(hierarchyPath, filepath.Dir(path))
		if err != nil {
			return err
		}

		// Get filename without extension
		filename := strings.TrimSuffix(filepath.Base(path), ".json")

		// Get the directory name
		dirname := filepath.Base(filepath.Dir(path))

		// Determine hierarchy key based on structure
		var hierarchyKey string
		if filename == dirname {
			// Nested structure: directory/directory.json → use directory path only
			// e.g., everything/everything.json → "everything"
			hierarchyKey = strings.ReplaceAll(relPath, string(filepath.Separator), ".")
			if hierarchyKey == "." {
				hierarchyKey = ""
			}
		} else {
			// Flat structure: directory/tool.json → use directory.tool
			// e.g., everything/add.json → "everything.add"
			dirKey := strings.ReplaceAll(relPath, string(filepath.Separator), ".")
			if dirKey == "." || dirKey == "" {
				hierarchyKey = filename
			} else {
				hierarchyKey = dirKey + "." + filename
			}
		}

		node, err := loadNode(path)
		if err != nil {
			log.Printf("Warning: failed to load node at %s: %v", path, err)
			return nil // Continue loading other nodes
		}

		h.nodes[hierarchyKey] = node
		log.Printf("Loaded hierarchy node: %s from %s", hierarchyKey, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk hierarchy: %w", err)
	}

	log.Printf("Loaded %d hierarchy nodes", len(h.nodes))
	return h, nil
}

// loadNode loads a single node from a JSON file
func loadNode(path string) (*HierarchyNode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var nodeData HierarchyNodeData
	if err := json.Unmarshal(data, &nodeData); err != nil {
		return nil, err
	}

	// Convert to HierarchyNode with typed tools
	node := &HierarchyNode{
		Overview:  nodeData.Overview,
		Tools:     make(map[string]*ToolDefinition),
		MCPServer: nodeData.MCPServer,
	}

	// Parse tools - can be either map[string]interface{} or direct ToolDefinition
	for toolName, toolData := range nodeData.Tools {
		if toolMap, ok := toolData.(map[string]interface{}); ok {
			tool := &ToolDefinition{}
			if desc, ok := toolMap["description"].(string); ok {
				tool.Description = desc
			}
			if mapsTo, ok := toolMap["maps_to"].(string); ok {
				tool.MapsTo = mapsTo
			} else {
				// Default maps_to is the tool name itself
				tool.MapsTo = toolName
			}
			if server, ok := toolMap["server"].(string); ok {
				tool.Server = server
			}
			if schema, ok := toolMap["inputSchema"].(map[string]interface{}); ok {
				tool.InputSchema = schema
			}
			node.Tools[toolName] = tool
		}
	}

	return node, nil
}

// GetRootNode returns the root node of the hierarchy
func (h *Hierarchy) GetRootNode() *HierarchyNode {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nodes[""]
}

// HandleGetToolsInCategory handles the get_tools_in_category meta-tool
// Returns a map with path, overview, children info, and tools
func (h *Hierarchy) HandleGetToolsInCategory(path string) (map[string]interface{}, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Normalize path
	if path == "/" {
		path = ""
	}
	path = strings.Trim(path, ".")

	// Find the node
	node, exists := h.nodes[path]
	if !exists {
		return nil, fmt.Errorf("category not found: %s", path)
	}

	// Build response
	response := map[string]interface{}{
		"path": path,
	}

	if node.Overview != "" {
		response["overview"] = node.Overview
	}

	// Find child nodes
	children := make(map[string]interface{})
	allChildrenAreLeaves := true
	aggregatedTools := make(map[string]interface{})

	for nodePath := range h.nodes {
		if nodePath == path || nodePath == "" {
			continue
		}

		// Check if this node is a direct child of the current path
		var isDirectChild bool
		var childName string

		if path == "" {
			// Root level - direct children have no dots
			if !strings.Contains(nodePath, ".") {
				isDirectChild = true
				childName = nodePath
			}
		} else {
			// Non-root - check if path is a prefix and child is one level deeper
			if strings.HasPrefix(nodePath, path+".") {
				remainder := strings.TrimPrefix(nodePath, path+".")
				if !strings.Contains(remainder, ".") {
					isDirectChild = true
					childName = remainder
				}
			}
		}

		if isDirectChild {
			childNode := h.nodes[nodePath]
			if len(childNode.Tools) > 0 {
				// Leaf node
				children[childName] = map[string]interface{}{
					"is_leaf":    true,
					"tool_count": len(childNode.Tools),
				}

				// Aggregate tools from leaf children
				for toolName, toolDef := range childNode.Tools {
					// In flat structure, nodePath already includes the tool name
					// e.g., "everything.echo" not "everything.echo.echo"
					toolPath := nodePath

					aggregatedTools[toolName] = map[string]interface{}{
						"description": toolDef.Description,
						"tool_path":   toolPath,
					}
				}
			} else {
				// Branch node
				allChildrenAreLeaves = false
				childInfo := map[string]interface{}{}
				if childNode.Overview != "" {
					childInfo["overview"] = childNode.Overview
				}
				children[childName] = childInfo
			}
		}
	}

	if len(children) > 0 {
		response["children"] = children
	}

	// If this node has direct tools or all children are leaves, include tools
	if len(node.Tools) > 0 {
		// Node has direct tools
		toolsInfo := make(map[string]interface{})
		for toolName, toolDef := range node.Tools {
			var toolPath string
			if path == "" {
				toolPath = toolName
			} else {
				toolPath = path + "." + toolName
			}

			toolsInfo[toolName] = map[string]interface{}{
				"description": toolDef.Description,
				"tool_path":   toolPath,
			}
		}
		response["tools"] = toolsInfo
	} else if allChildrenAreLeaves && len(aggregatedTools) > 0 {
		// All children are leaves - include their tools
		response["tools"] = aggregatedTools
	} else {
		response["tools"] = make(map[string]interface{})
	}

	return response, nil
}

// ResolveToolPath resolves a tool path to its definition and server name
// Returns the tool definition, server name (empty for meta-tools or if not configured), and any error
func (h *Hierarchy) ResolveToolPath(toolPath string) (*ToolDefinition, string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Parse the tool path
	parts := strings.Split(toolPath, ".")
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("invalid tool path: %s", toolPath)
	}

	var foundTool *ToolDefinition

	// Strategy 1: Check if the full path is a node, and look for a tool with the same name as the last part
	// e.g., "everything.echo" -> check node "everything.echo" for tool "echo"
	lastPart := parts[len(parts)-1]
	if node, exists := h.nodes[toolPath]; exists {
		if tool, ok := node.Tools[lastPart]; ok {
			foundTool = tool
		}
	}

	// Strategy 2: Try to find the tool by progressively trying longer paths
	// e.g., for "coding_tools.serena.search.find_symbol":
	// - Try "coding_tools.serena.search" with tool "find_symbol"
	// - Then "coding_tools.serena" with tool "find_symbol"
	// - Then "coding_tools" with tool "find_symbol"
	// - Finally "" (root) with tool "find_symbol"
	if foundTool == nil {
		// Start from longest path and work backwards
		for i := len(parts) - 1; i >= 0; i-- {
			var categoryPath string
			var toolName string

			if i == 0 {
				// Single part or trying root
				categoryPath = ""
				toolName = parts[0]
			} else {
				categoryPath = strings.Join(parts[:i], ".")
				toolName = parts[len(parts)-1]
			}

			if node, exists := h.nodes[categoryPath]; exists {
				// Check if this node has the tool
				if tool, ok := node.Tools[toolName]; ok {
					foundTool = tool
					break
				}
			}
		}
	}

	if foundTool == nil {
		return nil, "", fmt.Errorf("tool not found: %s", toolPath)
	}

	// Return the tool and its server name (from the tool-level server field)
	return foundTool, foundTool.Server, nil
}

// HandleExecuteTool handles the execute_tool meta-tool
func (h *Hierarchy) HandleExecuteTool(ctx context.Context, registry *ServerRegistry, toolPath string, arguments map[string]interface{}) (*mcp.CallToolResult, error) {
	// Resolve the tool path to get tool definition and server name
	toolDef, serverName, err := h.ResolveToolPath(toolPath)
	if err != nil {
		return nil, err
	}

	if serverName == "" {
		return nil, fmt.Errorf("no MCP server configured for tool: %s", toolPath)
	}

	// Bound the complete invocation, including lazy provider startup,
	// initialization, same-provider queuing, and the downstream call.
	toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Get or load the MCP client for this server
	client, err := registry.GetOrLoadServer(toolCtx, serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP client: %w", err)
	}

	// Use the mapped tool name
	actualToolName := toolDef.MapsTo
	if actualToolName == "" {
		actualToolName = strings.Split(toolPath, ".")[len(strings.Split(toolPath, "."))-1]
	}

	log.Printf("Executing tool: hierarchy_path=%s, server=%s, tool=%s", toolPath, serverName, actualToolName)

	// Serialize tool calls to the same server to prevent concurrent stdio access.
	// Stdio is a single-channel transport that cannot handle interleaved messages.
	// This protects mcp-go v0.43.2 stdio writes, which are not framed by a transport write mutex.
	mutex := registry.GetClientMutex(serverName)
	if err := mutex.LockContext(toolCtx); err != nil {
		return nil, fmt.Errorf("failed to wait for provider %s: %w", serverName, err)
	}
	defer mutex.Unlock()

	// Call the tool on the actual MCP server
	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = actualToolName
	callRequest.Params.Arguments = arguments

	result, err := client.CallTool(toolCtx, callRequest)
	if err != nil {
		// Include inputSchema in error message to help LLMs self-correct parameter mistakes
		if toolDef.InputSchema != nil {
			schemaJSON, marshalErr := json.MarshalIndent(toolDef.InputSchema, "", "  ")
			if marshalErr == nil {
				return nil, fmt.Errorf("failed to call tool %s: %w\n\nExpected inputSchema:\n%s", actualToolName, err, string(schemaJSON))
			}
		}
		return nil, fmt.Errorf("failed to call tool %s: %w", actualToolName, err)
	}

	// Check if result has IsError set - append schema to help LLMs self-correct
	if result != nil && result.IsError && toolDef.InputSchema != nil && len(result.Content) > 0 {
		schemaJSON, marshalErr := json.MarshalIndent(toolDef.InputSchema, "", "  ")
		if marshalErr == nil {
			// Append schema to the first text content item
			// Note: TextContent is a value type, so we modify the copy and assign it back to the slice
			if textContent, ok := result.Content[0].(mcp.TextContent); ok {
				textContent.Text += fmt.Sprintf("\n\nExpected inputSchema:\n%s", string(schemaJSON))
				result.Content[0] = textContent
			}
		}
	}
	return result, nil
}

// ServerRegistry manages MCP client connections
type ServerRegistry struct {
	clients       map[string]ProviderClient
	clientLoads   map[string]*providerLoad
	clientMutex   map[string]*ClientMutex // Per-client mutex for serializing tool calls
	serverConfigs map[string]*config.MCPClientConfigV2
	clientFactory ProviderClientFactory
	lifecycleCtx  context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	closed        bool
}

type providerLoad struct {
	done                     chan struct{}
	client                   ProviderClient
	err                      error
	initiatingCallerCanceled bool
}

var errProviderLoadCanceledByInitiator = errors.New("provider load canceled by initiating caller")

func markProviderLoadCanceledByInitiator(err error) error {
	return fmt.Errorf("%w: %w", errProviderLoadCanceledByInitiator, err)
}

// ProviderClient is the part of a downstream MCP client used by the hierarchy.
type ProviderClient interface {
	CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Close() error
	CloseFailed(context.Context) error
}

const failedProviderCleanupTimeout = 2 * time.Second

// ProviderClientFactory starts and initializes one downstream MCP client.
type ProviderClientFactory func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error)

// NewServerRegistry creates a new server registry with server configurations
func NewServerRegistry(serverConfigs map[string]*config.MCPClientConfigV2) *ServerRegistry {
	registry := newServerRegistry(serverConfigs, nil)
	registry.clientFactory = func(ctx context.Context, serverName string, cfg *config.MCPClientConfigV2) (ProviderClient, error) {
		return newProviderClient(ctx, registry.lifecycleCtx, serverName, cfg)
	}
	return registry
}

func newServerRegistry(serverConfigs map[string]*config.MCPClientConfigV2, factory ProviderClientFactory) *ServerRegistry {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return &ServerRegistry{
		clients:       make(map[string]ProviderClient),
		clientLoads:   make(map[string]*providerLoad),
		clientMutex:   make(map[string]*ClientMutex),
		serverConfigs: serverConfigs,
		clientFactory: factory,
		lifecycleCtx:  lifecycleCtx,
		cancel:        cancel,
	}
}

// GetClientMutex returns a mutex for the given server, creating one if needed.
// This mutex serializes tool calls to prevent concurrent stdio access.
// Note: This map grows with the number of unique servers accessed. Since the set of
// servers is bounded by the configuration/hierarchy, this is not a memory leak.
func (r *ServerRegistry) GetClientMutex(serverName string) *ClientMutex {
	r.mu.Lock()
	defer r.mu.Unlock()

	if m, exists := r.clientMutex[serverName]; exists {
		return m
	}

	m := NewClientMutex()
	r.clientMutex[serverName] = m
	return m
}

// ClientMutex serializes calls while allowing a queued call to stop waiting
// when its invocation context expires.
type ClientMutex struct {
	token chan struct{}
}

func NewClientMutex() *ClientMutex {
	mutex := &ClientMutex{token: make(chan struct{}, 1)}
	mutex.token <- struct{}{}
	return mutex
}

func (m *ClientMutex) Lock() {
	<-m.token
}

func (m *ClientMutex) LockContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}

func (m *ClientMutex) Unlock() {
	m.token <- struct{}{}
}

// GetOrLoadServer gets an existing client or creates and initializes a new one
// This implements lazy loading - servers are only started when first accessed
func (r *ServerRegistry) GetOrLoadServer(ctx context.Context, serverName string) (ProviderClient, error) {
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, errors.New("server registry is closed")
		}
		if client, exists := r.clients[serverName]; exists {
			r.mu.Unlock()
			return client, nil
		}
		if load, exists := r.clientLoads[serverName]; exists {
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-load.done:
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if load.initiatingCallerCanceled {
					continue
				}
				return load.client, load.err
			}
		}

		cfg, exists := r.serverConfigs[serverName]
		if !exists {
			r.mu.Unlock()
			return nil, fmt.Errorf("server config not found: %s", serverName)
		}
		load := &providerLoad{done: make(chan struct{})}
		r.clientLoads[serverName] = load
		r.mu.Unlock()

		mcpClient, err := r.clientFactory(ctx, serverName, cfg)
		initiatingCallerCanceled := errors.Is(err, errProviderLoadCanceledByInitiator)

		r.mu.Lock()
		if err == nil && r.closed {
			err = errors.New("server registry closed while provider was starting")
		}
		if err == nil {
			r.clients[serverName] = mcpClient
			load.client = mcpClient
		}
		load.err = err
		load.initiatingCallerCanceled = initiatingCallerCanceled
		delete(r.clientLoads, serverName)
		close(load.done)
		r.mu.Unlock()

		if err != nil && mcpClient != nil {
			closeFailedProvider(serverName, mcpClient, failedProviderCleanupTimeout)
			return nil, err
		}
		return mcpClient, err
	}
}

func newProviderClient(ctx, lifecycleCtx context.Context, serverName string, cfg *config.MCPClientConfigV2) (ProviderClient, error) {
	mcpClient, err := client.NewMCPClient(serverName, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	var startCtx *providerStartContext
	if mcpClient.NeedManualStart() {
		startCtx = newProviderStartContext(ctx, lifecycleCtx)
		if err := mcpClient.GetClient().Start(startCtx); err != nil {
			startCtx.abort()
			closeFailedProvider(serverName, mcpClient, failedProviderCleanupTimeout)
			if ctx.Err() != nil {
				return nil, fmt.Errorf("failed to start MCP client: %w", markProviderLoadCanceledByInitiator(ctx.Err()))
			}
			return nil, fmt.Errorf("failed to start MCP client: %w", err)
		}
		if ctx.Err() != nil {
			startCtx.abort()
			closeFailedProvider(serverName, mcpClient, failedProviderCleanupTimeout)
			return nil, fmt.Errorf("failed to start MCP client: %w", markProviderLoadCanceledByInitiator(ctx.Err()))
		}
		startCtx.detach()
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "tappet"}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err := mcpClient.GetClient().Initialize(ctx, initRequest); err != nil {
		if startCtx != nil {
			startCtx.abort()
		}
		closeFailedProvider(serverName, mcpClient, failedProviderCleanupTimeout)
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil, fmt.Errorf("failed to initialize MCP client: %w", markProviderLoadCanceledByInitiator(ctx.Err()))
		}
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	log.Printf("Created and initialized MCP client for provider: %s", serverName)
	if mcpClient.NeedPing() {
		go mcpClient.StartPingTask(lifecycleCtx)
	}
	return mcpClient, nil
}

// providerStartContext observes the invocation deadline until Start succeeds.
// After detach, the stable Done channel follows only the registry lifecycle,
// so an established SSE stream outlives the request that started it.
type providerStartContext struct {
	startup       context.Context
	lifecycle     context.Context
	done          chan struct{}
	mu            sync.Mutex
	err           error
	detached      bool
	stopStartup   func() bool
	stopLifecycle func() bool
}

func newProviderStartContext(startup, lifecycle context.Context) *providerStartContext {
	ctx := &providerStartContext{
		startup:   startup,
		lifecycle: lifecycle,
		done:      make(chan struct{}),
	}
	ctx.stopStartup = context.AfterFunc(startup, func() {
		ctx.cancelStartup(startup.Err())
	})
	ctx.stopLifecycle = context.AfterFunc(lifecycle, func() {
		ctx.cancel(lifecycle.Err())
	})
	return ctx
}

func (c *providerStartContext) Deadline() (time.Time, bool) {
	c.mu.Lock()
	detached := c.detached
	c.mu.Unlock()
	if detached {
		return c.lifecycle.Deadline()
	}
	return c.startup.Deadline()
}

func (c *providerStartContext) Done() <-chan struct{} { return c.done }

func (c *providerStartContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *providerStartContext) Value(key any) any {
	c.mu.Lock()
	detached := c.detached
	c.mu.Unlock()
	if detached {
		return c.lifecycle.Value(key)
	}
	return c.startup.Value(key)
}

func (c *providerStartContext) detach() {
	c.mu.Lock()
	c.detached = true
	c.mu.Unlock()
	c.stopStartup()
}

func (c *providerStartContext) abort() {
	c.stopStartup()
	c.stopLifecycle()
	c.cancel(context.Canceled)
}

func (c *providerStartContext) cancelStartup(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.detached || c.err != nil {
		return
	}
	c.err = err
	close(c.done)
}

func (c *providerStartContext) cancel(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	close(c.done)
}

// closeFailedProvider uses the provider's failure-specific, context-aware
// cleanup path. Stdio implementations kill and reap their child before return.
func closeFailedProvider(serverName string, provider ProviderClient, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := provider.CloseFailed(ctx); err != nil {
		log.Printf("Failed to close MCP client for provider %s after initialization failure: %v", serverName, err)
	}
}

// Close closes all clients in the registry
func (r *ServerRegistry) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.cancel()

	clients := r.clients
	r.clients = make(map[string]ProviderClient)
	r.clientLoads = make(map[string]*providerLoad)
	r.clientMutex = make(map[string]*ClientMutex)
	r.mu.Unlock()

	for name, client := range clients {
		log.Printf("Closing MCP client: %s", name)
		_ = client.Close()
	}
}
