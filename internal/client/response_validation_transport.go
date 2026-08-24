package client

import (
	"context"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type responseValidatingTransport struct {
	inner      transport.Interface
	validation *responseValidation
}

func newResponseValidatingTransport(inner transport.Interface, validation *responseValidation) *responseValidatingTransport {
	return &responseValidatingTransport{inner: inner, validation: validation}
}

func (t *responseValidatingTransport) Start(ctx context.Context) error {
	return t.inner.Start(ctx)
}

func (t *responseValidatingTransport) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		response *transport.JSONRPCResponse
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := t.inner.SendRequest(requestCtx, request)
		resultCh <- result{response: response, err: err}
	}()

	select {
	case <-t.validation.done:
		return nil, t.validation.err
	case value := <-resultCh:
		select {
		case <-t.validation.done:
			return nil, t.validation.err
		default:
		}
		return value.response, value.err
	case <-ctx.Done():
		select {
		case <-t.validation.done:
			return nil, t.validation.err
		default:
		}
		return nil, ctx.Err()
	}
}

func (t *responseValidatingTransport) SendNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	select {
	case <-t.validation.done:
		return t.validation.err
	default:
		return t.inner.SendNotification(ctx, notification)
	}
}

func (t *responseValidatingTransport) SetNotificationHandler(handler func(mcp.JSONRPCNotification)) {
	t.inner.SetNotificationHandler(handler)
}

func (t *responseValidatingTransport) Close() error {
	return t.inner.Close()
}

func (t *responseValidatingTransport) GetSessionId() string {
	return t.inner.GetSessionId()
}

func (t *responseValidatingTransport) SetProtocolVersion(version string) {
	if httpTransport, ok := t.inner.(transport.HTTPConnection); ok {
		httpTransport.SetProtocolVersion(version)
	}
}
