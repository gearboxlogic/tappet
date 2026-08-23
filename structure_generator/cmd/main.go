package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gearboxlogic/capscope/internal/client"
	"github.com/gearboxlogic/capscope/internal/config"
	generator "github.com/gearboxlogic/capscope/structure_generator"
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
	ListToolsByPage(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	Close() error
}

type catalogConnection struct {
	client    catalogClient
	needStart bool
}

type catalogClientFactory func(string, *config.MCPClientConfigV2) (catalogConnection, error)

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

			var serverTools generator.ServerTools
			if err := json.Unmarshal(data, &serverTools); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return fetchFromConfigWithFactory(ctx, configPath, newCatalogConnection)
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
	expanded.Args = make([]string, len(providerConfig.Args))
	for i, arg := range providerConfig.Args {
		expanded.Args[i] = os.ExpandEnv(arg)
	}
	return &expanded
}

// fetchToolsFromServer connects to an MCP server and fetches all tools
func fetchToolsFromServer(ctx context.Context, name string, providerConfig *config.MCPClientConfigV2, factory catalogClientFactory) (generator.ServerTools, error) {
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
		Name:    "capscope-structure-generator",
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

	log.Printf("[%s] Listing tools...", name)
	for {
		toolsResult, err := connection.client.ListToolsByPage(localCtx, toolsRequest)
		if err != nil {
			return generator.ServerTools{}, fmt.Errorf("failed to list tools: %w", err)
		}
		for _, mcpTool := range toolsResult.Tools {
			tool, err := convertTool(mcpTool)
			if err != nil {
				return generator.ServerTools{}, fmt.Errorf("failed to preserve metadata for tool %s: %w", mcpTool.Name, err)
			}
			allTools = append(allTools, tool)
		}
		if toolsResult.NextCursor == "" {
			break
		}
		toolsRequest.Params.Cursor = toolsResult.NextCursor
	}

	return generator.ServerTools{
		ServerName: name,
		Tools:      allTools,
	}, nil
}

func newCatalogConnection(name string, providerConfig *config.MCPClientConfigV2) (catalogConnection, error) {
	mcpClient, err := client.NewMCPClient(name, providerConfig)
	if err != nil {
		return catalogConnection{}, err
	}
	return catalogConnection{
		client:    mcpClient.GetClient(),
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

func convertTool(tool mcp.Tool) (generator.Tool, error) {
	data, err := json.Marshal(tool)
	if err != nil {
		return generator.Tool{}, err
	}
	var fields struct {
		Name         string                 `json:"name"`
		Description  string                 `json:"description"`
		InputSchema  map[string]interface{} `json:"inputSchema"`
		OutputSchema map[string]interface{} `json:"outputSchema"`
		Annotations  map[string]interface{} `json:"annotations"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return generator.Tool{}, err
	}
	title, _ := fields.Annotations["title"].(string)
	return generator.Tool{
		Name:         fields.Name,
		Title:        title,
		Description:  fields.Description,
		InputSchema:  fields.InputSchema,
		OutputSchema: fields.OutputSchema,
		Annotations:  fields.Annotations,
	}, nil
}
