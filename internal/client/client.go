package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gearboxlogic/capscope/internal/config"
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
}

func NewMCPClient(name string, conf *config.MCPClientConfigV2) (*Client, error) {
	return newMCPClient(name, conf, 0)
}

// NewMCPClientWithResponseLimit creates a client whose transport rejects
// downstream response frames larger than maxResponseBytes before JSON decoding.
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
		if maxResponseBytes > 0 {
			stdioTransport := newLimitedStdioTransport(value.Command, envs, value.Args, maxResponseBytes)
			return &Client{
				name:            name,
				needManualStart: true,
				client:          client.NewClient(stdioTransport),
			}, nil
		}
		mcpClient, err := client.NewStdioMCPClient(value.Command, envs, value.Args...)
		if err != nil {
			return nil, err
		}
		return &Client{name: name, client: mcpClient}, nil

	case *config.SSEMCPClientConfig:
		var options []transport.ClientOption
		if maxResponseBytes > 0 {
			options = append(options, client.WithHTTPClient(newResponseLimitedHTTPClient(http.DefaultTransport, 0, maxResponseBytes)))
		}
		if len(value.Headers) > 0 {
			options = append(options, client.WithHeaders(value.Headers))
		}
		mcpClient, err := client.NewSSEMCPClient(value.URL, options...)
		if err != nil {
			return nil, err
		}
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
		}, nil

	case *config.StreamableMCPClientConfig:
		var options []transport.StreamableHTTPCOption
		if maxResponseBytes > 0 {
			options = append(options, transport.WithHTTPBasicClient(newResponseLimitedHTTPClient(http.DefaultTransport, value.Timeout, maxResponseBytes)))
		}
		if len(value.Headers) > 0 {
			options = append(options, transport.WithHTTPHeaders(value.Headers))
		}
		if value.Timeout > 0 && maxResponseBytes == 0 {
			options = append(options, transport.WithHTTPTimeout(value.Timeout))
		}
		mcpClient, err := client.NewStreamableHttpClient(value.URL, options...)
		if err != nil {
			return nil, err
		}
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
		}, nil
	}
	return nil, errors.New("invalid client type")
}

func (c *Client) startPingTask(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	failCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("<%s> Context done, stopping ping", c.name)
			return
		case <-ticker.C:
			if err := c.client.Ping(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
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

// CallTool invokes a tool through the downstream MCP client.
func (c *Client) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return c.client.CallTool(ctx, request)
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
