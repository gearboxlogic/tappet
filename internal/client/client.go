package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Client adapts the configured downstream MCP transports used by the current
// hierarchy broker. Provider startup and lifetime are owned by ServerRegistry.
type Client struct {
	name            string
	needPing        bool
	needManualStart bool
	client          *client.Client
	closeFailed     func(context.Context) error
	toolMu          sync.RWMutex
	invalidTools    map[string]error
}

const (
	providerPingInterval = 30 * time.Second
	providerPingTimeout  = 10 * time.Second
	// maxProviderMessageBytes is the V1 downstream protocol-message ceiling.
	// It applies to every provider transport before mcp-go decodes JSON.
	maxProviderMessageBytes int64 = 16 << 20
)

func NewMCPClient(name string, conf *config.MCPClientConfigV2) (*Client, error) {
	return newMCPClient(name, conf, maxProviderMessageBytes)
}

// NewMCPClientWithResponseLimit creates a client whose transport rejects each
// downstream response frame or SSE event larger than maxResponseBytes before
// JSON decoding.
func NewMCPClientWithResponseLimit(name string, conf *config.MCPClientConfigV2, maxResponseBytes int64) (*Client, error) {
	if maxResponseBytes <= 0 {
		return nil, errors.New("maximum response bytes must be positive")
	}
	return newMCPClient(name, conf, maxResponseBytes)
}

func newMCPClient(name string, conf *config.MCPClientConfigV2, maxResponseBytes int64) (*Client, error) {
	clientInfo, err := config.ParseMCPClientConfigV2(conf)
	if err != nil {
		return nil, err
	}
	switch value := clientInfo.(type) {
	case *config.StdioMCPClientConfig:
		envs := make([]string, 0, len(value.Env))
		for key, val := range value.Env {
			envs = append(envs, fmt.Sprintf("%s=%s", key, val))
		}
		stdioTransport := newLimitedStdioTransport(value.Command, envs, value.Args, maxResponseBytes)
		validatingTransport := newResponseValidatingTransport(stdioTransport, stdioTransport.validation)
		return &Client{
			name:            name,
			needManualStart: true,
			client:          client.NewClient(validatingTransport),
			closeFailed: func(ctx context.Context) error {
				validatingTransport.events.stop()
				return stdioTransport.CloseFailed(ctx)
			},
		}, nil

	case *config.SSEMCPClientConfig:
		var options []transport.ClientOption
		validation := newResponseValidation()
		options = append(options, client.WithHTTPClient(newResponseLimitedHTTPClientWithValidation(http.DefaultTransport, 0, maxResponseBytes, validation)))
		if len(value.Headers) > 0 {
			options = append(options, client.WithHeaders(value.Headers))
		}
		sseTransport, err := transport.NewSSE(value.URL, options...)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE transport: %w", err)
		}
		mcpClient := client.NewClient(newResponseValidatingTransport(sseTransport, validation))
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
			closeFailed:     func(context.Context) error { return mcpClient.Close() },
		}, nil

	case *config.StreamableMCPClientConfig:
		var options []transport.StreamableHTTPCOption
		validation := newResponseValidation()
		options = append(options, transport.WithHTTPBasicClient(newResponseLimitedHTTPClientWithValidation(http.DefaultTransport, value.Timeout, maxResponseBytes, validation)))
		if len(value.Headers) > 0 {
			options = append(options, transport.WithHTTPHeaders(value.Headers))
		}
		streamableTransport, err := transport.NewStreamableHTTP(value.URL, options...)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE transport: %w", err)
		}
		mcpClient := client.NewClient(newResponseValidatingTransport(streamableTransport, validation))
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
			closeFailed:     func(context.Context) error { return mcpClient.Close() },
		}, nil
	}
	return nil, errors.New("invalid client type")
}

func (c *Client) startPingTask(ctx context.Context) {
	c.runPingTask(ctx, providerPingInterval, providerPingTimeout, c.client.Ping)
}

func (c *Client) runPingTask(ctx context.Context, interval, timeout time.Duration, ping func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	failCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("<%s> Context done, stopping ping", c.name)
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, timeout)
			err := ping(pingCtx)
			cancel()
			if ctx.Err() != nil {
				log.Printf("<%s> Context done, stopping ping", c.name)
				return
			}
			if err != nil {
				failCount++
				log.Printf("<%s> MCP Ping failed: %v (count=%d)", c.name, err, failCount)
			} else if failCount > 0 {
				log.Printf("<%s> MCP Ping recovered after %d failures", c.name, failCount)
				failCount = 0
			}
		}
	}
}

func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// CloseFailed terminates a provider that did not finish startup. Stdio clients
// kill and reap their child directly so mcp-go cannot block waiting for a child
// that is still holding its pipes open.
func (c *Client) CloseFailed(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closeFailed != nil {
		return c.closeFailed(ctx)
	}
	return c.Close()
}

// CallTool invokes a tool through the downstream MCP client.
func (c *Client) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c.toolMu.RLock()
	invalid := c.invalidTools[request.Params.Name]
	c.toolMu.RUnlock()
	if invalid != nil {
		return nil, fmt.Errorf("provider tool %q has invalid x-mcp-header annotations: %w", request.Params.Name, invalid)
	}
	return c.client.CallTool(ctx, request)
}

// ListTools returns only provider tools whose x-mcp-header annotations are
// safe to use. A provider must not make an invalid definition callable merely
// by listing it beside valid tools.
func (c *Client) ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	result, err := c.client.ListTools(ctx, request)
	if err != nil {
		return nil, err
	}
	valid := result.Tools[:0]
	invalid := make(map[string]error)
	for i := range result.Tools {
		tool := &result.Tools[i]
		if err := mcp.ValidateParamHeaderAnnotations(tool); err != nil {
			invalid[tool.Name] = err
			continue
		}
		valid = append(valid, *tool)
	}
	result.Tools = valid
	c.toolMu.Lock()
	c.invalidTools = invalid
	c.toolMu.Unlock()
	return result, nil
}

// GetClient returns the underlying MCP client.
func (c *Client) GetClient() *client.Client {
	return c.client
}

// NeedManualStart reports whether the transport needs an explicit Start call.
func (c *Client) NeedManualStart() bool {
	return c.needManualStart
}

// NeedPing reports whether the transport uses the inherited keepalive task.
func (c *Client) NeedPing() bool {
	return c.needPing
}

// StartPingTask pings until the provider lifecycle context is canceled.
func (c *Client) StartPingTask(ctx context.Context) {
	c.startPingTask(ctx)
}
