package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

func TestResponseLimitedRoundTripperBoundsUndeclaredBody(t *testing.T) {
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

	assert.Equal(t, "01234567", string(data))
	require.ErrorIs(t, readErr, ErrResponseLimitExceeded)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

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
