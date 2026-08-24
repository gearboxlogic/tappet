package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	tappetclient "github.com/gearboxlogic/tappet/internal/client"
	"github.com/gearboxlogic/tappet/internal/config"
	generator "github.com/gearboxlogic/tappet/structure_generator"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return strings.Join(*i, ", ")
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// Config represents the MCP server configuration
type Config struct {
	MCPServers map[string]*config.MCPClientConfigV2 `json:"mcpServers"`
	OutputDir  string                               `json:"outputDir,omitempty"`
}

type catalogClient interface {
	Start(context.Context) error
	Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListToolsByPage(context.Context, mcp.ListToolsRequest) (*catalogToolsPage, error)
	Close() error
}

type catalogToolsPage struct {
	NextCursor mcp.Cursor        `json:"nextCursor,omitempty"`
	Tools      []json.RawMessage `json:"tools"`
}

func (p *catalogToolsPage) UnmarshalJSON(data []byte) error {
	if err := tappetclient.RejectDuplicateJSONMembers(data); err != nil {
		return fmt.Errorf("invalid tools/list result: %w", err)
	}

	var wire struct {
		NextCursor mcp.Cursor      `json:"nextCursor,omitempty"`
		Tools      json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	encodedTools := bytes.TrimSpace(wire.Tools)
	if len(encodedTools) == 0 || bytes.Equal(encodedTools, []byte("null")) {
		return errors.New(`tools/list result is missing required "tools" array`)
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(encodedTools, &tools); err != nil {
		return fmt.Errorf(`tools/list result field "tools" must be an array: %w`, err)
	}
	p.NextCursor = wire.NextCursor
	p.Tools = tools
	return nil
}

type transportCatalogClient struct {
	client    *mcpclient.Client
	requestID atomic.Uint64
}

func (c *transportCatalogClient) Start(ctx context.Context) error {
	return c.client.Start(ctx)
}

func (c *transportCatalogClient) Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return c.client.Initialize(ctx, request)
}

func (c *transportCatalogClient) ListToolsByPage(ctx context.Context, request mcp.ListToolsRequest) (*catalogToolsPage, error) {
	response, err := c.client.GetTransport().SendRequest(ctx, mcptransport.JSONRPCRequest{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(fmt.Sprintf("tappet-inventory-%d", c.requestID.Add(1))),
		Method:  "tools/list",
		Params:  request.Params,
		Header:  request.Header,
	})
	if err != nil {
		return nil, mcptransport.NewError(err)
	}
	if response.Error != nil {
		return nil, response.Error.AsError()
	}

	var result catalogToolsPage
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to decode lossless tools/list result: %w", err)
	}
	return &result, nil
}

func (c *transportCatalogClient) Close() error {
	return c.client.Close()
}

type catalogConnection struct {
	client    catalogClient
	needStart bool
}

type catalogClientFactory func(string, *config.MCPClientConfigV2) (catalogConnection, error)

const (
	maxInventoryPages     = 128
	maxInventoryTools     = 4_096
	maxInventoryPageBytes = 4 << 20
	maxInventoryBytes     = 32 << 20
)

type inventoryLimits struct {
	pages      int
	tools      int
	pageBytes  int
	totalBytes int
}

func defaultInventoryLimits() inventoryLimits {
	return inventoryLimits{
		pages:      maxInventoryPages,
		tools:      maxInventoryTools,
		pageBytes:  maxInventoryPageBytes,
		totalBytes: maxInventoryBytes,
	}
}

func main() {
	var inputFiles arrayFlags
	flag.Var(&inputFiles, "input", "Path to tool JSON file (can be specified multiple times)")
	outputDir := flag.String("output", "./structure", "Output directory for generated structure")
	configPath := flag.String("config", "", "Path to MCP server config JSON (to fetch tools from live servers)")
	regenerateRoot := flag.Bool("regenerate", false, "Regenerate hierarchy from existing structure (preserves manual edits)")
	flag.Parse()

	// Mode 0: Regenerate hierarchy
	if *regenerateRoot {
		log.Printf("Regenerating hierarchy (preserves manual edits) in: %s", *outputDir)
		if err := generator.Regenerate(*outputDir); err != nil {
			log.Fatalf("Failed to regenerate: %v", err)
		}
		fmt.Printf("\n✓ Successfully regenerated hierarchy!\n")
		fmt.Printf("  Location: %s\n", *outputDir)
		os.Exit(0)
	}

	var servers []generator.ServerTools

	// Mode 1: Using config file to fetch from live MCP servers
	if *configPath != "" {
		log.Printf("Loading config from: %s", *configPath)
		configServers, err := fetchFromConfig(*configPath)
		if err != nil {
			log.Fatalf("Failed to fetch from config: %v", err)
		}
		servers = configServers

		// Use outputDir from config if not specified via flag
		if *outputDir == "./structure" {
			configData, _ := os.ReadFile(*configPath)
			var config Config
			if json.Unmarshal(configData, &config) == nil && config.OutputDir != "" {
				*outputDir = config.OutputDir
			}
		}
	} else if len(inputFiles) > 0 {
		// Mode 2: Using pre-fetched JSON files
		for _, inputFile := range inputFiles {
			data, err := os.ReadFile(inputFile)
			if err != nil {
				log.Fatalf("Failed to read %s: %v", inputFile, err)
			}

			serverTools, err := decodeServerTools(data)
			if err != nil {
				log.Fatalf("Failed to parse %s: %v", inputFile, err)
			}

			servers = append(servers, serverTools)
			log.Printf("Loaded: %s (%d tools)", serverTools.ServerName, len(serverTools.Tools))
		}
	} else {
		log.Fatal("Usage:\n" +
			"  Mode 1 (fetch from live servers):  go run cmd/main.go -config <config.json>\n" +
			"  Mode 2 (use pre-fetched data):     go run cmd/main.go -input <file1.json> -input <file2.json>\n" +
			"  Mode 3 (regenerate hierarchy):     go run cmd/main.go -regenerate -output <structure_dir>\n\n" +
			"Examples:\n" +
			"  go run cmd/main.go -config tests/test_data/test_config.json\n" +
			"  go run cmd/main.go -input tests/test_data/github_tools.json -input tests/test_data/everything_tools.json\n" +
			"  go run cmd/main.go -regenerate -output ./structure")
	}

	if len(servers) == 0 {
		log.Fatal("No servers loaded")
	}

	// Generate structure
	log.Printf("\nGenerating structure to: %s", *outputDir)
	if err := generator.GenerateStructure(servers, *outputDir); err != nil {
		log.Fatalf("Failed to generate structure: %v", err)
	}

	// Print summary
	totalTools := 0
	for _, server := range servers {
		totalTools += len(server.Tools)
	}

	fmt.Printf("\n✓ Successfully generated structure!\n")
	fmt.Printf("  Location: %s\n", *outputDir)
	fmt.Printf("  Servers: %d\n", len(servers))
	fmt.Printf("  Total tools: %d\n\n", totalTools)

	fmt.Println("Generated structure:")
	fmt.Printf("%s/\n", *outputDir)
	fmt.Println("├── root.json")
	for i, server := range servers {
		if i == len(servers)-1 {
			fmt.Printf("└── %s/\n", server.ServerName)
			fmt.Printf("    └── %s.json (%d tools)\n", server.ServerName, len(server.Tools))
		} else {
			fmt.Printf("├── %s/\n", server.ServerName)
			fmt.Printf("│   └── %s.json (%d tools)\n", server.ServerName, len(server.Tools))
		}
	}

	// Explicitly exit to terminate any hanging stdio processes
	os.Exit(0)
}

// fetchFromConfig loads config and fetches tools from all MCP servers
func fetchFromConfig(configPath string) ([]generator.ServerTools, error) {
	return fetchFromConfigWithFactory(context.Background(), configPath, newCatalogConnection)
}

func fetchFromConfigWithFactory(ctx context.Context, configPath string, factory catalogClientFactory) ([]generator.ServerTools, error) {
	// Read config file
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	serverNames := make([]string, 0, len(config.MCPServers))
	for serverName := range config.MCPServers {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)

	allServers := make([]generator.ServerTools, 0, len(serverNames))

	// Fetch from each server
	for _, serverName := range serverNames {
		serverConfig := config.MCPServers[serverName]
		if serverConfig == nil {
			return nil, fmt.Errorf("invalid provider config for %s: configuration is null", serverName)
		}
		serverConfig = expandProviderConfig(serverConfig)
		log.Printf("Connecting to MCP server: %s", serverName)

		serverTools, err := fetchToolsFromServer(ctx, serverName, serverConfig, factory)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch complete tool inventory from %s: %w", serverName, err)
		}

		allServers = append(allServers, serverTools)
		log.Printf("✓ Fetched %d tools from %s", len(serverTools.Tools), serverName)
	}

	return allServers, nil
}

func expandProviderConfig(providerConfig *config.MCPClientConfigV2) *config.MCPClientConfigV2 {
	expanded := *providerConfig
	expanded.Command = os.ExpandEnv(providerConfig.Command)
	expanded.URL = os.ExpandEnv(providerConfig.URL)
	expanded.Args = make([]string, len(providerConfig.Args))
	for i, arg := range providerConfig.Args {
		expanded.Args[i] = os.ExpandEnv(arg)
	}
	if providerConfig.Env != nil {
		expanded.Env = make(map[string]string, len(providerConfig.Env))
		for name, value := range providerConfig.Env {
			expanded.Env[name] = os.ExpandEnv(value)
		}
	}
	if providerConfig.Headers != nil {
		expanded.Headers = make(map[string]string, len(providerConfig.Headers))
		for name, value := range providerConfig.Headers {
			expanded.Headers[name] = os.ExpandEnv(value)
		}
	}
	return &expanded
}

// fetchToolsFromServer connects to an MCP server and fetches all tools
func fetchToolsFromServer(ctx context.Context, name string, providerConfig *config.MCPClientConfigV2, factory catalogClientFactory) (generator.ServerTools, error) {
	return fetchToolsFromServerWithLimits(ctx, name, providerConfig, factory, defaultInventoryLimits())
}

func fetchToolsFromServerWithLimits(ctx context.Context, name string, providerConfig *config.MCPClientConfigV2, factory catalogClientFactory, limits inventoryLimits) (generator.ServerTools, error) {
	connection, err := factory(name, providerConfig)
	if err != nil {
		return generator.ServerTools{}, fmt.Errorf("failed to create client: %w", err)
	}
	defer closeCatalogClientAsync(name, connection.client)

	log.Printf("[%s] Client created, initializing...", name)

	localCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if connection.needStart {
		if err := connection.client.Start(localCtx); err != nil {
			return generator.ServerTools{}, fmt.Errorf("failed to start client: %w", err)
		}
	}

	// Initialize connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "tappet-structure-generator",
		Version: "0.1.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err := connection.client.Initialize(localCtx, initRequest); err != nil {
		return generator.ServerTools{}, fmt.Errorf("failed to initialize: %w", err)
	}

	log.Printf("[%s] Initialized successfully", name)

	// Fetch all tools
	var allTools []generator.Tool
	toolsRequest := mcp.ListToolsRequest{}
	seenCursors := make(map[mcp.Cursor]struct{})
	pageCount := 0
	totalBytes := 2 // JSON array brackets.

	log.Printf("[%s] Listing tools...", name)
	for {
		if pageCount >= limits.pages {
			return generator.ServerTools{}, fmt.Errorf("failed to list tools: inventory page limit exceeded: maximum %d pages", limits.pages)
		}
		pageCount++

		toolsResult, err := connection.client.ListToolsByPage(localCtx, toolsRequest)
		if err != nil {
			return generator.ServerTools{}, fmt.Errorf("failed to list tools: %w", err)
		}
		if toolsResult == nil {
			return generator.ServerTools{}, errors.New("failed to list tools: provider returned no tools/list result")
		}
		if len(toolsResult.Tools) > limits.tools-len(allTools) {
			return generator.ServerTools{}, fmt.Errorf("failed to list tools: inventory tool limit exceeded: maximum %d tools", limits.tools)
		}
		if toolsResult.NextCursor != "" {
			if _, seen := seenCursors[toolsResult.NextCursor]; seen {
				return generator.ServerTools{}, fmt.Errorf("failed to list tools: repeated pagination cursor %q", toolsResult.NextCursor)
			}
		}

		pageTools := make([]generator.Tool, 0, len(toolsResult.Tools))
		pageBytes := 2 // JSON array brackets.
		for toolIndex, rawTool := range toolsResult.Tools {
			tool, err := convertTool(rawTool)
			if err != nil {
				return generator.ServerTools{}, fmt.Errorf("failed to preserve metadata for tool at index %d: %w", toolIndex, err)
			}
			encodedTool, err := json.Marshal(tool)
			if err != nil {
				return generator.ServerTools{}, fmt.Errorf("failed to measure metadata for tool %s: %w", tool.Name, err)
			}
			pageSeparator := 0
			if len(pageTools) > 0 {
				pageSeparator = 1
			}
			if exceedsInventoryLimit(pageBytes, len(encodedTool)+pageSeparator, limits.pageBytes) {
				return generator.ServerTools{}, fmt.Errorf("failed to list tools: inventory page byte limit exceeded: maximum %d encoded bytes", limits.pageBytes)
			}
			totalSeparator := 0
			if len(allTools)+len(pageTools) > 0 {
				totalSeparator = 1
			}
			if exceedsInventoryLimit(totalBytes, len(encodedTool)+totalSeparator, limits.totalBytes) {
				return generator.ServerTools{}, fmt.Errorf("failed to list tools: inventory byte limit exceeded: maximum %d encoded bytes", limits.totalBytes)
			}
			pageBytes += len(encodedTool) + pageSeparator
			totalBytes += len(encodedTool) + totalSeparator
			pageTools = append(pageTools, tool)
		}
		allTools = append(allTools, pageTools...)
		if toolsResult.NextCursor == "" {
			break
		}
		seenCursors[toolsResult.NextCursor] = struct{}{}
		toolsRequest.Params.Cursor = toolsResult.NextCursor
	}

	return generator.ServerTools{
		ServerName: name,
		Tools:      allTools,
	}, nil
}

func exceedsInventoryLimit(current, additional, limit int) bool {
	return current > limit || additional > limit-current
}

func newCatalogConnection(name string, providerConfig *config.MCPClientConfigV2) (catalogConnection, error) {
	mcpClient, err := tappetclient.NewMCPClientWithResponseLimit(name, providerConfig, maxInventoryPageBytes)
	if err != nil {
		return catalogConnection{}, err
	}
	return catalogConnection{
		client:    &transportCatalogClient{client: mcpClient.GetClient()},
		needStart: mcpClient.NeedManualStart(),
	}, nil
}

func closeCatalogClientAsync(name string, catalog catalogClient) {
	go func() {
		if err := catalog.Close(); err != nil {
			log.Printf("[%s] Failed to close catalog client: %v", name, err)
		}
	}()
}

func convertTool(data json.RawMessage) (generator.Tool, error) {
	if err := tappetclient.RejectDuplicateJSONMembers(data); err != nil {
		return generator.Tool{}, fmt.Errorf("invalid tool metadata: %w", err)
	}
	var fields struct {
		Name         string                 `json:"name"`
		Title        string                 `json:"title"`
		Description  string                 `json:"description"`
		InputSchema  map[string]interface{} `json:"inputSchema"`
		OutputSchema map[string]interface{} `json:"outputSchema"`
		Annotations  map[string]interface{} `json:"annotations"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return generator.Tool{}, err
	}
	if fields.InputSchema == nil {
		return generator.Tool{}, fmt.Errorf("tool %q is missing required object inputSchema", fields.Name)
	}
	title := fields.Title
	if title == "" {
		title, _ = fields.Annotations["title"].(string)
	}
	return generator.Tool{
		Name:         fields.Name,
		Title:        title,
		Description:  fields.Description,
		InputSchema:  fields.InputSchema,
		OutputSchema: fields.OutputSchema,
		Annotations:  fields.Annotations,
	}, nil
}

func decodeServerTools(data []byte) (generator.ServerTools, error) {
	if err := tappetclient.RejectDuplicateJSONMembers(data); err != nil {
		return generator.ServerTools{}, fmt.Errorf("invalid pre-fetched server metadata: %w", err)
	}

	var serverTools generator.ServerTools
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&serverTools); err != nil {
		return generator.ServerTools{}, err
	}
	return serverTools, nil
}
