package client

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type responseValidatingTransport struct {
	inner      transport.Interface
	validation *responseValidation
	events     *providerEventQueue
}

func newResponseValidatingTransport(inner transport.Interface, validation *responseValidation) *responseValidatingTransport {
	t := &responseValidatingTransport{inner: inner, validation: validation}
	t.events = newProviderEventQueue(validation, func() { _ = inner.Close() })
	return t
}

func (t *responseValidatingTransport) Start(ctx context.Context) error {
	if err := t.inner.Start(ctx); err != nil {
		return err
	}
	t.events.start()
	return nil
}

func (t *responseValidatingTransport) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	for attempt := range 2 {
		response, err := t.sendRequestOnce(ctx, request)
		if err != nil || response == nil || response.Error == nil {
			return response, err
		}

		rpcErr := t.validation.takeRPCError(response.ID)
		if response.Error.Code == mcp.UNSUPPORTED_PROTOCOL_VERSION {
			var data mcp.UnsupportedProtocolVersionData
			var ok bool
			if rpcErr != nil {
				data, ok = unsupportedVersionData(rpcErr.Data)
			}
			if !ok {
				data, ok = unsupportedVersionData(response.Error.Data)
			}
			if ok {
				// A modern peer may reject a request while naming that same
				// revision as supported. Retry once at the transport boundary;
				// mcp-go otherwise falls back to the removed initialize flow.
				if attempt == 0 && slices.Contains(data.Supported, data.Requested) {
					continue
				}
				return nil, mcp.UnsupportedProtocolVersionError{
					Version:   data.Requested,
					Supported: data.Supported,
				}
			}
		}
		if request.Method == string(mcp.MethodToolsCall) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			data, _ := json.Marshal(response.Error.Data)
			return nil, &ProviderRPCError{
				Code:    response.Error.Code,
				Message: response.Error.Message,
				Data:    data,
			}
		}
		return response, nil
	}
	return nil, mcp.UnsupportedProtocolVersionError{}
}

func (t *responseValidatingTransport) sendRequestOnce(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
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

func unsupportedVersionData(value any) (mcp.UnsupportedProtocolVersionData, bool) {
	var encoded []byte
	switch raw := value.(type) {
	case json.RawMessage:
		encoded = raw
	case []byte:
		encoded = raw
	default:
		encoded, _ = json.Marshal(value)
	}
	var data mcp.UnsupportedProtocolVersionData
	if len(encoded) == 0 || json.Unmarshal(encoded, &data) != nil || len(data.Supported) == 0 {
		return mcp.UnsupportedProtocolVersionData{}, false
	}
	return data, true
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
	t.events.setHandler(handler)
	t.inner.SetNotificationHandler(t.events.enqueue)
}

func (t *responseValidatingTransport) Close() error {
	t.events.stop()
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
