package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedLineReaderRejectsFrameBeforeRelease(t *testing.T) {
	var limitSignals atomic.Int32
	reader := newBoundedLineReader(
		strings.NewReader("0123456789\n"),
		8,
		func() { limitSignals.Add(1) },
	)

	data, err := io.ReadAll(reader)

	assert.Empty(t, data)
	require.ErrorIs(t, err, ErrResponseLimitExceeded)
	assert.Equal(t, int32(1), limitSignals.Load())
}

func TestBoundedLineReaderResetsLimitForEachFrame(t *testing.T) {
	reader := newBoundedLineReader(strings.NewReader("123\n456\n"), 4, nil)

	data, err := io.ReadAll(reader)

	require.NoError(t, err)
	assert.Equal(t, "123\n456\n", string(data))
}

func TestResponseLimitedRoundTripperRejectsDeclaredOversize(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("oversized")}
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          body,
				ContentLength: 9,
				Header:        make(http.Header),
			}, nil
		}),
		maxBytes: 8,
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))

	assert.Nil(t, response)
	require.ErrorIs(t, err, ErrResponseLimitExceeded)
	assert.True(t, body.closed.Load())
}

func TestResponseLimitedRoundTripperRejectsUndeclaredOversizeBeforeRelease(t *testing.T) {
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader("0123456789")),
				ContentLength: -1,
				Header:        make(http.Header),
			}, nil
		}),
		maxBytes: 8,
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	assert.Empty(t, data)
	require.ErrorIs(t, readErr, ErrResponseLimitExceeded)
}

func TestResponseLimitedRoundTripperRejectsDuplicateJSONBeforeRelease(t *testing.T) {
	validation := newResponseValidation()
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{},"result":{}}`)),
				ContentLength: -1,
				Header:        http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
		maxBytes:   1_024,
		validation: validation,
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	assert.Empty(t, data)
	require.ErrorIs(t, readErr, ErrDuplicateJSONMember)
}

func TestResponseLimitedRoundTripperResetsLimitForEachSSEEvent(t *testing.T) {
	event := "data: 1234\r\n\r\n"
	stream := event + event
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(stream)),
				ContentLength: int64(len(stream)),
				Header: http.Header{
					"Content-Type": []string{"text/event-stream; charset=utf-8"},
				},
			}, nil
		}),
		maxBytes: int64(len(event)),
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	require.NoError(t, readErr)
	assert.Equal(t, stream, string(data))
}

func TestResponseLimitedRoundTripperResetsLimitAtBareCRSSEBoundaries(t *testing.T) {
	event := "data: 1234\r\r"
	stream := event + event
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(stream)),
				ContentLength: int64(len(stream)),
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
			}, nil
		}),
		maxBytes: int64(len(event)),
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	require.NoError(t, readErr)
	assert.Equal(t, stream, string(data))
}

func TestResponseLimitedRoundTripperRejectsOversizedSSEEventBeforeRelease(t *testing.T) {
	event := "data: 12345\n\n"
	body := &trackingReadCloser{Reader: strings.NewReader(event)}
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          body,
				ContentLength: -1,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
			}, nil
		}),
		maxBytes: int64(len(event) - 1),
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	assert.Empty(t, data)
	require.ErrorIs(t, readErr, ErrResponseLimitExceeded)
	assert.True(t, body.closed.Load())
}

func TestResponseLimitedRoundTripperRejectsDuplicateSSEJSONBeforeRelease(t *testing.T) {
	event := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{},\"result\":{}}\n\n"
	validation := newResponseValidation()
	roundTripper := responseLimitedRoundTripper{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(event)),
				ContentLength: int64(len(event)),
				Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
			}, nil
		}),
		maxBytes:   int64(len(event)),
		validation: validation,
	}

	response, err := roundTripper.RoundTrip(mustRequest(t))
	require.NoError(t, err)
	data, readErr := io.ReadAll(response.Body)

	assert.Empty(t, data)
	require.ErrorIs(t, readErr, ErrDuplicateJSONMember)
}

func TestLimitedStdioClientDefersProcessStart(t *testing.T) {
	mcpClient, err := NewMCPClientWithResponseLimit("bounded", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStdio,
		Command:       "command-that-does-not-exist",
	}, 1_024)

	require.NoError(t, err)
	assert.True(t, mcpClient.NeedManualStart())
	require.NoError(t, mcpClient.Close())
}

func TestLimitedStdioTransportRetainsInboundHandlersUntilStart(t *testing.T) {
	stdioTransport := newLimitedStdioTransport("unused", nil, nil, 1_024)
	notificationHandler := func(mcp.JSONRPCNotification) {}
	requestHandler := func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
		return nil, nil
	}
	stdioTransport.SetNotificationHandler(notificationHandler)
	stdioTransport.SetRequestHandler(requestHandler)

	recorder := &inboundHandlerRecorder{}
	installInboundHandlers(recorder, stdioTransport.notificationHandler, stdioTransport.requestHandler)

	assert.NotNil(t, recorder.notificationHandler)
	assert.NotNil(t, recorder.requestHandler)
}

func TestResponseValidatingTransportDoesNotAdvertiseUnsupportedCallbacks(t *testing.T) {
	stdioTransport := newLimitedStdioTransport("unused", nil, nil, 1_024)
	validatingTransport := newResponseValidatingTransport(stdioTransport, stdioTransport.validation)

	_, bidirectional := any(validatingTransport).(transport.BidirectionalInterface)
	assert.False(t, bidirectional)
}

func TestProviderEventQueueOverflowIsBoundedAndClosesConnection(t *testing.T) {
	validation := newResponseValidation()
	var closed atomic.Int32
	queue := newProviderEventQueue(validation, func() { closed.Add(1) })
	block := make(chan struct{})
	var active atomic.Int32
	queue.setHandler(func(mcp.JSONRPCNotification) {
		active.Add(1)
		<-block
	})
	queue.start()
	t.Cleanup(func() {
		close(block)
		queue.stop()
	})

	for range maxProviderEventHandlers {
		queue.enqueue(mcp.JSONRPCNotification{})
	}
	require.Eventually(t, func() bool {
		return active.Load() == maxProviderEventHandlers
	}, time.Second, time.Millisecond)
	for range maxProviderQueuedEvents + 1 {
		queue.enqueue(mcp.JSONRPCNotification{})
	}

	select {
	case <-validation.done:
		require.ErrorIs(t, validation.err, ErrProviderEventOverflow)
	case <-time.After(time.Second):
		t.Fatal("event overflow did not fail the provider connection")
	}
	require.Eventually(t, func() bool { return closed.Load() == 1 }, time.Second, time.Millisecond)
}

func TestProviderEventQueueByteOverflowClosesConnection(t *testing.T) {
	validation := newResponseValidation()
	var closed atomic.Int32
	queue := newProviderEventQueue(validation, func() { closed.Add(1) })
	block := make(chan struct{})
	queue.setHandler(func(mcp.JSONRPCNotification) { <-block })
	queue.start()
	t.Cleanup(func() {
		close(block)
		queue.stop()
	})

	for range maxProviderEventHandlers {
		queue.enqueue(mcp.JSONRPCNotification{})
	}
	large := mcp.JSONRPCNotification{Notification: mcp.Notification{
		Method: strings.Repeat("x", maxProviderQueuedEventBytes/2),
	}}
	queue.enqueue(large)
	queue.enqueue(large)

	select {
	case <-validation.done:
		require.ErrorIs(t, validation.err, ErrProviderEventOverflow)
	case <-time.After(time.Second):
		t.Fatal("event byte overflow did not fail the provider connection")
	}
	require.Eventually(t, func() bool { return closed.Load() == 1 }, time.Second, time.Millisecond)
}

func TestResponseValidatingTransportPreservesToolRPCError(t *testing.T) {
	validation := newResponseValidation()
	require.NoError(t, validateJSONMessage([]byte(`{"jsonrpc":"2.0","id":7,"error":{"code":-32042,"message":"denied","data":{"account":9007199254740993}}}`), validation))
	inner := &fixedResponseTransport{response: &transport.JSONRPCResponse{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(7),
		Error: &mcp.JSONRPCErrorDetails{
			Code:    -32042,
			Message: "denied",
			Data:    map[string]any{"account": float64(9007199254740993)},
		},
	}}
	validating := newResponseValidatingTransport(inner, validation)

	response, err := validating.SendRequest(t.Context(), transport.JSONRPCRequest{
		ID:     mcp.NewRequestId(7),
		Method: string(mcp.MethodToolsCall),
	})

	assert.Nil(t, response)
	var rpcErr *ProviderRPCError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, -32042, rpcErr.Code)
	assert.Equal(t, "denied", rpcErr.Message)
	assert.JSONEq(t, `{"account":9007199254740993}`, string(rpcErr.Data))
}

func TestResponseValidatingTransportPreservesUnsupportedVersionData(t *testing.T) {
	validation := newResponseValidation()
	require.NoError(t, validateJSONMessage([]byte(`{"jsonrpc":"2.0","id":8,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2026-07-28"],"requested":"2099-01-01"}}}`), validation))
	inner := &fixedResponseTransport{response: &transport.JSONRPCResponse{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(8),
		Error: &mcp.JSONRPCErrorDetails{
			Code:    mcp.UNSUPPORTED_PROTOCOL_VERSION,
			Message: "unsupported",
			Data:    json.RawMessage(`{"supported":["2026-07-28"],"requested":"2099-01-01"}`),
		},
	}}
	validating := newResponseValidatingTransport(inner, validation)

	response, err := validating.SendRequest(t.Context(), transport.JSONRPCRequest{
		ID:     mcp.NewRequestId(8),
		Method: string(mcp.MethodServerDiscover),
	})

	assert.Nil(t, response)
	var unsupported mcp.UnsupportedProtocolVersionError
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, "2099-01-01", unsupported.Version)
	assert.Equal(t, []string{"2026-07-28"}, unsupported.Supported)
}

func TestStreamableClientRetriesModernDiscoveryAfterVersionRejection(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&envelope))
		assert.Equal(t, string(mcp.MethodServerDiscover), envelope.Method)
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32022,"message":"unsupported","data":{"supported":["2026-07-28"],"requested":"2026-07-28"}}}`, envelope.ID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"supportedVersions":["2026-07-28"],"capabilities":{},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"fixture","version":"1"}}}}`, envelope.ID)
	}))
	t.Cleanup(provider.Close)

	mcpClient, err := NewMCPClient("retry", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStreamable,
		URL:           provider.URL,
	})
	require.NoError(t, err)
	require.NoError(t, mcpClient.GetClient().Start(t.Context()))
	t.Cleanup(func() { _ = mcpClient.Close() })
	initialize := mcp.InitializeRequest{}
	initialize.Params.ProtocolVersion = mcp.ProtocolVersion20260728
	initialize.Params.ClientInfo = mcp.Implementation{Name: "fixture", Version: "1"}
	result, err := mcpClient.GetClient().Initialize(t.Context(), initialize)

	require.NoError(t, err)
	assert.Equal(t, mcp.ProtocolVersion20260728, result.ProtocolVersion)
	assert.Equal(t, int32(2), requests.Load())
}

func TestLimitedStdioTransportRejectsOversizeBeforeDecode(t *testing.T) {
	if os.Getenv("TAPPET_LIMIT_FIXTURE") == "1" {
		var request string
		_, _ = fmt.Fscanln(os.Stdin, &request)
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"payload\":\"%s\"}}\n", strings.Repeat("x", 256))
		return
	}

	stdioTransport := newLimitedStdioTransport(
		os.Args[0],
		[]string{"TAPPET_LIMIT_FIXTURE=1"},
		[]string{"-test.run=^TestLimitedStdioTransportRejectsOversizeBeforeDecode$"},
		128,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, stdioTransport.Start(ctx))
	t.Cleanup(func() { require.NoError(t, stdioTransport.Close()) })

	response, err := stdioTransport.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(1),
		Method:  "fixture/oversize",
	})

	assert.Nil(t, response)
	require.ErrorIs(t, err, ErrResponseLimitExceeded)
}

func TestLimitedStdioClientRejectsDuplicateEnvelopeBeforeDecode(t *testing.T) {
	if os.Getenv("TAPPET_DUPLICATE_FIXTURE") == "1" {
		var request string
		_, _ = fmt.Fscanln(os.Stdin, &request)
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{},"result":{"tools":[]}}`)
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}

	mcpClient, err := NewMCPClientWithResponseLimit("duplicate", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStdio,
		Command:       os.Args[0],
		Args:          []string{"-test.run=^TestLimitedStdioClientRejectsDuplicateEnvelopeBeforeDecode$"},
		Env:           map[string]string{"TAPPET_DUPLICATE_FIXTURE": "1"},
	}, 1_024)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, mcpClient.GetClient().Start(ctx))
	t.Cleanup(func() { _ = mcpClient.Close() })

	response, err := mcpClient.GetClient().GetTransport().SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: mcp.JSONRPC_VERSION,
		ID:      mcp.NewRequestId(1),
		Method:  "fixture/duplicate",
	})

	assert.Nil(t, response)
	require.ErrorIs(t, err, ErrDuplicateJSONMember)
}

func TestStdioProviderRejectsUnsolicitedCallbackPromptly(t *testing.T) {
	if os.Getenv("TAPPET_CALLBACK_FIXTURE") == "1" {
		runUnsolicitedCallbackFixture()
		return
	}

	mcpClient, err := NewMCPClient("callback", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStdio,
		Command:       os.Args[0],
		Args:          []string{"-test.run=^TestStdioProviderRejectsUnsolicitedCallbackPromptly$"},
		Env:           map[string]string{"TAPPET_CALLBACK_FIXTURE": "1"},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, mcpClient.GetClient().Start(ctx))
	t.Cleanup(func() { _ = mcpClient.Close() })

	initialize := mcp.InitializeRequest{}
	initialize.Params.ClientInfo = mcp.Implementation{Name: "tappet-test", Version: "1"}
	_, err = mcpClient.GetClient().Initialize(ctx, initialize)
	require.NoError(t, err)
	request := mcp.CallToolRequest{}
	request.Params.Name = "callback-test"
	result, err := mcpClient.CallTool(ctx, request)

	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	content, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)
	assert.Equal(t, "callback rejected", content.Text)
}

func runUnsolicitedCallbackFixture() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		switch request.Method {
		case string(mcp.MethodServerDiscover):
			_ = encoder.Encode(map[string]any{
				"jsonrpc": mcp.JSONRPC_VERSION,
				"id":      request.ID,
				"error": map[string]any{
					"code":    mcp.METHOD_NOT_FOUND,
					"message": "legacy provider",
				},
			})
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": mcp.JSONRPC_VERSION,
				"id":      request.ID,
				"result": map[string]any{
					"protocolVersion": mcp.ProtocolVersion20251125,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "callback-fixture", "version": "1"},
				},
			})
		case string(mcp.MethodToolsCall):
			_ = encoder.Encode(map[string]any{
				"jsonrpc": mcp.JSONRPC_VERSION,
				"id":      "callback-1",
				"method":  string(mcp.MethodSamplingCreateMessage),
				"params":  map[string]any{},
			})
			if !scanner.Scan() {
				return
			}
			var callbackResponse struct {
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(scanner.Bytes(), &callbackResponse) != nil ||
				callbackResponse.Error == nil || callbackResponse.Error.Code != mcp.METHOD_NOT_FOUND {
				return
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": mcp.JSONRPC_VERSION,
				"id":      request.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "callback rejected"}},
				},
			})
			return
		}
	}
}

func TestAwaitLimitedStdioResponsePrefersLimitSignal(t *testing.T) {
	for range 100 {
		limitCh := make(chan struct{})
		responseCh := make(chan limitedStdioResponse, 1)
		responseCh <- limitedStdioResponse{err: io.EOF}
		close(limitCh)

		response, err := awaitLimitedStdioResponse(context.Background(), limitCh, responseCh)
		assert.Nil(t, response)
		require.ErrorIs(t, err, ErrResponseLimitExceeded)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type fixedResponseTransport struct {
	mu                  sync.Mutex
	response            *transport.JSONRPCResponse
	notificationHandler func(mcp.JSONRPCNotification)
}

func (*fixedResponseTransport) Start(context.Context) error { return nil }

func (t *fixedResponseTransport) SendRequest(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	return t.response, nil
}

func (*fixedResponseTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error {
	return nil
}

func (t *fixedResponseTransport) SetNotificationHandler(handler func(mcp.JSONRPCNotification)) {
	t.mu.Lock()
	t.notificationHandler = handler
	t.mu.Unlock()
}

func (*fixedResponseTransport) Close() error         { return nil }
func (*fixedResponseTransport) GetSessionId() string { return "" }

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

type inboundHandlerRecorder struct {
	notificationHandler func(mcp.JSONRPCNotification)
	requestHandler      transport.RequestHandler
}

func (r *inboundHandlerRecorder) SetNotificationHandler(handler func(mcp.JSONRPCNotification)) {
	r.notificationHandler = handler
}

func (r *inboundHandlerRecorder) SetRequestHandler(handler transport.RequestHandler) {
	r.requestHandler = handler
}

func (r *trackingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func mustRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://provider.invalid/mcp", nil)
	require.NoError(t, err)
	return request
}

func TestNewMCPClientWithResponseLimitRejectsInvalidLimit(t *testing.T) {
	client, err := NewMCPClientWithResponseLimit("invalid", &config.MCPClientConfigV2{}, 0)

	assert.Nil(t, client)
	require.EqualError(t, err, "maximum response bytes must be positive")
}
