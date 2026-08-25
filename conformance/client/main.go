package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"slices"

	tappetclient "github.com/gearboxlogic/tappet/internal/client"
	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

type scenarioHandler func(context.Context, *tappetclient.Client, map[string]any) error

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <server-url>", os.Args[0])
	}
	scenario := os.Getenv("MCP_CONFORMANCE_SCENARIO")
	handler := scenarios()[scenario]
	if handler == nil {
		log.Fatalf("scenario %q is outside Tappet's downstream provider scope", scenario)
	}

	ctx := context.Background()
	client, err := connect(ctx, os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	if err := handler(ctx, client, conformanceContext()); err != nil {
		log.Fatalf("scenario %q: %v", scenario, err)
	}
}

func connect(ctx context.Context, endpoint string) (*tappetclient.Client, error) {
	client, err := tappetclient.NewMCPClient("conformance", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStreamable,
		URL:           endpoint,
	})
	if err != nil {
		return nil, err
	}
	if err := client.GetClient().Start(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "tappet-conformance", Version: "1"}
	request.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := client.GetClient().Initialize(ctx, request); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func scenarios() map[string]scenarioHandler {
	return map[string]scenarioHandler{
		"initialize":                       listTools,
		"tools_call":                       callAddNumbers,
		"request-metadata":                 noOp,
		"sse-retry":                        reconnectTool,
		"sep-2322-client-request-state":    exerciseUnsupportedMRTR,
		"http-standard-headers":            exerciseStandardHeaders,
		"http-custom-headers":              exerciseCustomHeaders,
		"http-invalid-tool-headers":        exerciseInvalidHeaderTools,
		"json-schema-ref-no-deref":         listTools,
		"json-schema-2020-12-preservation": listTools,
	}
}

func noOp(context.Context, *tappetclient.Client, map[string]any) error { return nil }

func listTools(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	_, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	return err
}

func callAddNumbers(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	listed, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return err
	}
	if slices.IndexFunc(listed.Tools, func(tool mcp.Tool) bool { return tool.Name == "add_numbers" }) < 0 {
		return fmt.Errorf("tool add_numbers not found")
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = "add_numbers"
	request.Params.Arguments = map[string]any{"a": 5, "b": 3}
	_, err = client.CallTool(ctx, request)
	return err
}

func reconnectTool(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	request := mcp.CallToolRequest{}
	request.Params.Name = "test_reconnection"
	request.Params.Arguments = map[string]any{}
	_, err := client.CallTool(ctx, request)
	return err
}

func exerciseUnsupportedMRTR(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	for _, name := range []string{
		"test_mrtr_echo_state",
		"test_mrtr_no_state",
		"test_mrtr_unrelated",
		"test_mrtr_no_result_type",
	} {
		request := mcp.CallToolRequest{}
		request.Params.Name = name
		request.Params.Arguments = map[string]any{}
		_, _ = client.CallTool(ctx, request)
	}
	return nil
}

func exerciseStandardHeaders(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	if listed, err := client.ListTools(ctx, mcp.ListToolsRequest{}); err == nil && len(listed.Tools) > 0 {
		request := mcp.CallToolRequest{}
		request.Params.Name = listed.Tools[0].Name
		request.Params.Arguments = map[string]any{}
		_, _ = client.CallTool(ctx, request)
	}
	if listed, err := client.GetClient().ListResources(ctx, mcp.ListResourcesRequest{}); err == nil && len(listed.Resources) > 0 {
		request := mcp.ReadResourceRequest{}
		request.Params.URI = listed.Resources[0].URI
		_, _ = client.GetClient().ReadResource(ctx, request)
	}
	if listed, err := client.GetClient().ListPrompts(ctx, mcp.ListPromptsRequest{}); err == nil && len(listed.Prompts) > 0 {
		request := mcp.GetPromptRequest{}
		request.Params.Name = listed.Prompts[0].Name
		request.Params.Arguments = map[string]string{}
		_, _ = client.GetClient().GetPrompt(ctx, request)
	}
	return nil
}

func exerciseCustomHeaders(ctx context.Context, client *tappetclient.Client, values map[string]any) error {
	if _, err := client.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		return err
	}
	for _, call := range toolCalls(values) {
		request := mcp.CallToolRequest{}
		request.Params.Name = call.name
		request.Params.Arguments = call.arguments
		_, _ = client.CallTool(ctx, request)
	}
	return nil
}

func exerciseInvalidHeaderTools(ctx context.Context, client *tappetclient.Client, _ map[string]any) error {
	listed, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return err
	}
	for _, tool := range listed.Tools {
		request := mcp.CallToolRequest{}
		request.Params.Name = tool.Name
		request.Params.Arguments = map[string]any{"region": "us-west1"}
		_, _ = client.CallTool(ctx, request)
	}
	return nil
}

type toolCall struct {
	name      string
	arguments map[string]any
}

func toolCalls(values map[string]any) []toolCall {
	raw, _ := values["toolCalls"].([]any)
	result := make([]toolCall, 0, len(raw))
	for _, item := range raw {
		object, _ := item.(map[string]any)
		name, _ := object["name"].(string)
		arguments, _ := object["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		result = append(result, toolCall{name: name, arguments: arguments})
	}
	return result
}

func conformanceContext() map[string]any {
	var values map[string]any
	_ = json.Unmarshal([]byte(os.Getenv("MCP_CONFORMANCE_CONTEXT")), &values)
	return values
}
