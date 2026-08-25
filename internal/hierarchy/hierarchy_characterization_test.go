package hierarchy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tappetclient "github.com/gearboxlogic/tappet/internal/client"
	"github.com/gearboxlogic/tappet/internal/config"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHierarchyTraversalAndResolution(t *testing.T) {
	h := loadCharacterizationHierarchy(t)

	root, err := h.HandleGetToolsInCategory("")
	require.NoError(t, err)
	assert.Equal(t, "", root["path"])
	assert.Contains(t, root["children"], "alpha")

	nested, err := h.HandleGetToolsInCategory("alpha.nested")
	require.NoError(t, err)
	tools := nested["tools"].(map[string]interface{})
	require.Contains(t, tools, "public_tool")
	assert.Equal(t, "alpha.nested.public_tool", tools["public_tool"].(map[string]interface{})["tool_path"])

	_, err = h.HandleGetToolsInCategory("alpha.missing")
	assert.EqualError(t, err, "category not found: alpha.missing")

	tool, provider, err := h.ResolveToolPath("alpha.nested.public_tool")
	require.NoError(t, err)
	assert.Equal(t, "provider-a", provider)
	assert.Equal(t, "actual_tool", tool.MapsTo)

	_, _, err = h.ResolveToolPath("alpha.nested.public_tool.extra")
	assert.EqualError(t, err, "tool not found: alpha.nested.public_tool.extra")

	first, err := json.Marshal(root)
	require.NoError(t, err)
	second, err := json.Marshal(root)
	require.NoError(t, err)
	assert.Equal(t, first, second, "encoding/json provides deterministic map-key ordering")
}

func TestProviderLifecycleIsLazyAndReusable(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	recorder := &lifecycleRecorder{}
	registry := newServerRegistry(characterizationConfigs(), recordingFactory(recorder, func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recorder.recordCall(request.Params.Name)
		return mcp.NewToolResultText("ok"), nil
	}))

	assert.Empty(t, recorder.snapshot(), "loading Tappet must not start a provider")
	_, err := h.HandleGetToolsInCategory("alpha.nested")
	require.NoError(t, err)
	assert.Empty(t, recorder.snapshot(), "browsing must not start a provider")

	for range 2 {
		result, err := h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", map[string]interface{}{"value": "x"})
		require.NoError(t, err)
		require.False(t, result.IsError)
	}

	assert.Equal(t, []string{"start:provider-a", "initialize:provider-a", "call:actual_tool", "call:actual_tool"}, recorder.snapshot())
	registry.Close()
	assert.Equal(t, []string{"start:provider-a", "initialize:provider-a", "call:actual_tool", "call:actual_tool", "close:provider-a"}, recorder.snapshot())
}

func TestOrdinaryStdioProviderStartsBeforeInitialize(t *testing.T) {
	const fixtureEnv = "TAPPET_STDIO_START_FIXTURE"
	if os.Getenv(fixtureEnv) == "1" {
		fixture := mcpserver.NewMCPServer("stdio-start-fixture", "test")
		_ = mcpserver.ServeStdio(fixture)
		os.Exit(0)
	}

	executable, err := os.Executable()
	require.NoError(t, err)
	registry := NewServerRegistry(map[string]*config.MCPClientConfigV2{
		"provider-a": {
			TransportType: config.MCPClientTypeStdio,
			Command:       executable,
			Args:          []string{"-test.run=^TestOrdinaryStdioProviderStartsBeforeInitialize$"},
			Env:           map[string]string{fixtureEnv: "1"},
		},
	})
	t.Cleanup(registry.Close)

	provider, err := registry.GetOrLoadServer(context.Background(), "provider-a")
	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestProviderClientNegotiatesModernAndLegacyStreamableHTTP(t *testing.T) {
	tests := []struct {
		name        string
		options     []mcpserver.StreamableHTTPOption
		wantVersion string
		wantModern  bool
	}{
		{
			name:        "modern stateless provider",
			wantVersion: mcp.ProtocolVersion20260728,
			wantModern:  true,
		},
		{
			name: "legacy-only provider",
			options: []mcpserver.StreamableHTTPOption{
				mcpserver.WithStreamableHTTPProtocolVersions(mcp.LegacyProtocolVersions()...),
			},
			wantVersion: mcp.ProtocolVersion20251125,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			downstream := mcpserver.NewMCPServer("provider-fixture", "test")
			downstream.AddTool(mcp.NewTool("actual_tool"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				assert.Equal(t, test.wantVersion, mcpserver.RequestProtocolVersion(ctx))
				assert.Equal(t, test.wantModern, mcpserver.IsModernRequest(ctx))
				if test.wantModern {
					info := mcpserver.RequestProtocolInfoFromContext(ctx)
					require.NotNil(t, info)
					require.NotNil(t, info.ClientCapabilities)
					assert.Nil(t, info.ClientCapabilities.Sampling)
					assert.Nil(t, info.ClientCapabilities.Elicitation)
					assert.Nil(t, info.ClientCapabilities.Roots)
				}
				return mcp.NewToolResultText(test.wantVersion), nil
			})
			httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(downstream, test.options...))
			t.Cleanup(httpServer.Close)

			lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
			t.Cleanup(cancelLifecycle)
			provider, err := newProviderClient(t.Context(), lifecycleCtx, "provider-fixture", &config.MCPClientConfigV2{
				TransportType: config.MCPClientTypeStreamable,
				URL:           httpServer.URL,
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, provider.Close()) })

			request := mcp.CallToolRequest{}
			request.Params.Name = "actual_tool"
			result, err := provider.CallTool(t.Context(), request)
			require.NoError(t, err)
			require.Len(t, result.Content, 1)
			assert.Equal(t, test.wantVersion, result.Content[0].(mcp.TextContent).Text)
		})
	}
}

func TestProviderClientPreservesTypedToolResult(t *testing.T) {
	downstream := mcpserver.NewMCPServer("typed-result-provider", "test")
	downstream.AddTool(mcp.NewTool("actual_tool"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("provider result"),
			},
			StructuredContent: map[string]any{
				"status": "denied",
				"count":  json.Number("9007199254740993"),
			},
			IsError: true,
		}
		result.Meta = mcp.NewMetaFromMap(map[string]any{"provider-trace": "trace-123"})
		return result, nil
	})
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(downstream))
	t.Cleanup(httpServer.Close)

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	t.Cleanup(cancelLifecycle)
	provider, err := newProviderClient(t.Context(), lifecycleCtx, "typed-result-provider", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStreamable,
		URL:           httpServer.URL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	request := mcp.CallToolRequest{}
	request.Params.Name = "actual_tool"
	result, err := provider.CallTool(t.Context(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, mcp.ResultTypeComplete, result.ResultType)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "provider result", result.Content[0].(mcp.TextContent).Text)
	assert.JSONEq(t, `{"status":"denied","count":9007199254740993}`, string(result.RawStructuredContent))
	require.NotNil(t, result.Meta)
	assert.Equal(t, "trace-123", result.Meta.AdditionalFields["provider-trace"])
}

func TestProviderClientRejectsUnsupportedMultiRoundTripInput(t *testing.T) {
	downstream := mcpserver.NewMCPServer("callback-provider", "test")
	downstream.AddTool(mcp.NewTool("actual_tool"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Result: mcp.Result{ResultType: mcp.ResultTypeInputRequired},
			MultiRoundTripResult: mcp.MultiRoundTripResult{
				InputRequests: mcp.InputRequests{
					"sample": mcp.NewSamplingInputRequest(mcp.CreateMessageParams{}),
				},
				RequestState: "opaque-provider-state",
			},
		}, nil
	})
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(downstream))
	t.Cleanup(httpServer.Close)

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	t.Cleanup(cancelLifecycle)
	provider, err := newProviderClient(t.Context(), lifecycleCtx, "callback-provider", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStreamable,
		URL:           httpServer.URL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	request := mcp.CallToolRequest{}
	request.Params.Name = "actual_tool"
	started := time.Now()
	result, err := provider.CallTool(t.Context(), request)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sampling handler configured")
	assert.Less(t, time.Since(started), time.Second)
}

func TestSSEProviderReceivesConfiguredHeadersAndOutlivesTriggeringRequest(t *testing.T) {
	const authorization = "Bearer sse-provider-credential-539dbc"
	var receivedAuthorization atomic.Bool
	downstream := mcpserver.NewMCPServer("sse-provider", "test")
	downstream.AddTool(mcp.NewTool("actual_tool"), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	testServer := mcpserver.NewTestServer(downstream, mcpserver.WithSSEContextFunc(func(ctx context.Context, request *http.Request) context.Context {
		if request.Header.Get("Authorization") == authorization {
			receivedAuthorization.Store(true)
		}
		return ctx
	}))
	t.Cleanup(testServer.Close)

	configs := characterizationConfigs()
	configs["provider-a"] = &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeSSE,
		URL:           testServer.URL + "/sse",
		Headers:       map[string]string{"Authorization": authorization},
	}
	registry := NewServerRegistry(configs)
	defer registry.Close()
	h := loadCharacterizationHierarchy(t)

	triggerCtx, cancelTrigger := context.WithCancel(context.Background())
	result, err := h.HandleExecuteTool(triggerCtx, registry, "alpha.nested.public_tool", nil)
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.True(t, receivedAuthorization.Load())
	cancelTrigger()

	// The triggering request's derived invocation context is canceled when the
	// first call returns. Give the SSE receive loop time to observe cancellation
	// before proving that the cached provider still serves another request.
	time.Sleep(50 * time.Millisecond)
	result, err = h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", nil)
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestInvocationDeadlineIncludesProviderAcquisition(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	registry := newServerRegistry(characterizationConfigs(), func(ctx context.Context, _ string, _ *config.MCPClientConfigV2) (ProviderClient, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	defer registry.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := h.HandleExecuteTool(ctx, registry, "alpha.nested.public_tool", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "failed to get MCP client")
}

func TestInvocationDeadlineStopsPendingSSEProviderStartup(t *testing.T) {
	requestStarted := make(chan struct{})
	requestClosed := make(chan struct{})
	var startedOnce sync.Once
	var closedOnce sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(requestStarted) })
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		closedOnce.Do(func() { close(requestClosed) })
	}))
	t.Cleanup(provider.Close)

	configs := characterizationConfigs()
	configs["provider-a"] = &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeSSE,
		URL:           provider.URL + "/sse",
	}
	registry := NewServerRegistry(configs)
	defer registry.Close()
	h := loadCharacterizationHierarchy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := h.HandleExecuteTool(ctx, registry, "alpha.nested.public_tool", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "failed to get MCP client")

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("SSE provider did not receive the startup request")
	}
	select {
	case <-requestClosed:
	case <-time.After(time.Second):
		t.Fatal("SSE startup request remained open after the invocation deadline")
	}
}

func TestFailedProviderCleanupDoesNotBlockOtherProviderLoads(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	closeDone := make(chan struct{})
	failedProvider := &blockingCloseProvider{
		closeStarted: closeStarted,
		release:      releaseClose,
		closeDone:    closeDone,
	}
	initializeErr := errors.New("initialize failed")
	registry := newServerRegistry(characterizationConfigs(), func(_ context.Context, name string, _ *config.MCPClientConfigV2) (ProviderClient, error) {
		if name == "provider-a" {
			closeFailedProvider(name, failedProvider, time.Second)
			return nil, initializeErr
		}
		return ProviderClientFunc(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		}), nil
	})
	defer registry.Close()

	providerAResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		providerAResult <- err
	}()
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("failed-provider cleanup did not start")
	}

	loaded := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-b")
		loaded <- err
	}()
	select {
	case err := <-loaded:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("a hanging failed-provider cleanup blocked another provider load")
	}

	close(releaseClose)
	assert.ErrorIs(t, <-providerAResult, initializeErr)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("failed-provider cleanup did not finish after release")
	}
}

func TestFailedProviderCleanupHonorsDeadline(t *testing.T) {
	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	provider := &blockingCloseProvider{
		closeStarted: closeStarted,
		release:      make(chan struct{}),
		closeDone:    closeDone,
	}

	started := time.Now()
	closeFailedProvider("blocked-provider", provider, 20*time.Millisecond)

	assert.Less(t, time.Since(started), time.Second)
	select {
	case <-closeStarted:
	default:
		t.Fatal("failed-provider cleanup did not start")
	}
	select {
	case <-closeDone:
	default:
		t.Fatal("failed-provider cleanup did not stop at its deadline")
	}
}

func TestSlowProviderLoadDoesNotBlockOtherProviders(t *testing.T) {
	providerAStarted := make(chan struct{})
	releaseProviderA := make(chan struct{})
	registry := newServerRegistry(characterizationConfigs(), func(_ context.Context, name string, _ *config.MCPClientConfigV2) (ProviderClient, error) {
		if name == "provider-a" {
			close(providerAStarted)
			<-releaseProviderA
		}
		return ProviderClientFunc(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(name), nil
		}), nil
	})
	defer registry.Close()

	providerAResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		providerAResult <- err
	}()
	select {
	case <-providerAStarted:
	case <-time.After(time.Second):
		t.Fatal("provider-a did not begin loading")
	}

	providerBResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-b")
		providerBResult <- err
	}()
	select {
	case err := <-providerBResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("provider-a startup blocked provider-b")
	}

	close(releaseProviderA)
	require.NoError(t, <-providerAResult)
}

func TestConcurrentCallsShareOneProviderLoad(t *testing.T) {
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	var loadCount int
	var loadMu sync.Mutex
	provider := &staticProvider{}
	registry := newServerRegistry(characterizationConfigs(), func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		loadMu.Lock()
		loadCount++
		if loadCount == 1 {
			close(loadStarted)
		}
		loadMu.Unlock()
		<-releaseLoad
		return provider, nil
	})
	defer registry.Close()

	type loadResult struct {
		client ProviderClient
		err    error
	}
	results := make(chan loadResult, 2)
	for range 2 {
		go func() {
			client, err := registry.GetOrLoadServer(context.Background(), "provider-a")
			results <- loadResult{client: client, err: err}
		}()
	}
	<-loadStarted
	time.Sleep(20 * time.Millisecond)
	close(releaseLoad)

	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Same(t, first.client, second.client)
	loadMu.Lock()
	assert.Equal(t, 1, loadCount)
	loadMu.Unlock()
}

func TestLiveWaiterRetriesLoadCanceledByInitiatingCaller(t *testing.T) {
	firstLoadStarted := make(chan struct{})
	var loadCount int
	var loadMu sync.Mutex
	provider := &staticProvider{}
	registry := newServerRegistry(characterizationConfigs(), func(ctx context.Context, _ string, _ *config.MCPClientConfigV2) (ProviderClient, error) {
		loadMu.Lock()
		loadCount++
		currentLoad := loadCount
		loadMu.Unlock()
		if currentLoad == 1 {
			close(firstLoadStarted)
			<-ctx.Done()
			return nil, markProviderLoadCanceledByInitiator(ctx.Err())
		}
		return provider, nil
	})
	defer registry.Close()

	initiatorCtx, cancelInitiator := context.WithCancel(context.Background())
	initiatorResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(initiatorCtx, "provider-a")
		initiatorResult <- err
	}()
	<-firstLoadStarted

	waiterResult := make(chan struct {
		client ProviderClient
		err    error
	}, 1)
	go func() {
		client, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		waiterResult <- struct {
			client ProviderClient
			err    error
		}{client: client, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	cancelInitiator()

	assert.ErrorIs(t, <-initiatorResult, context.Canceled)
	waiter := <-waiterResult
	require.NoError(t, waiter.err)
	assert.Same(t, provider, waiter.client)
	loadMu.Lock()
	assert.Equal(t, 2, loadCount)
	loadMu.Unlock()
}

func TestLiveWaiterPreservesProviderLocalLoadTimeout(t *testing.T) {
	firstLoadStarted := make(chan struct{})
	releaseFirstLoad := make(chan struct{})
	var loadCount int
	var loadMu sync.Mutex
	unexpectedRetry := errors.New("provider load retried")
	registry := newServerRegistry(characterizationConfigs(), func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		loadMu.Lock()
		loadCount++
		currentLoad := loadCount
		loadMu.Unlock()
		if currentLoad != 1 {
			return nil, unexpectedRetry
		}
		close(firstLoadStarted)
		<-releaseFirstLoad
		return nil, context.DeadlineExceeded
	})
	defer registry.Close()

	initiatorResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		initiatorResult <- err
	}()
	<-firstLoadStarted

	waiterResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		waiterResult <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(releaseFirstLoad)

	assert.ErrorIs(t, <-initiatorResult, context.DeadlineExceeded)
	assert.ErrorIs(t, <-waiterResult, context.DeadlineExceeded)
	loadMu.Lock()
	assert.Equal(t, 1, loadCount)
	loadMu.Unlock()
}

func TestLiveWaiterPreservesUnmarkedProviderTimeoutFromExpiredInitiator(t *testing.T) {
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	var loadCount int
	var loadMu sync.Mutex
	unwantedRetry := errors.New("provider load retried")
	registry := newServerRegistry(characterizationConfigs(), func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		loadMu.Lock()
		loadCount++
		currentLoad := loadCount
		loadMu.Unlock()
		if currentLoad != 1 {
			return nil, unwantedRetry
		}
		close(loadStarted)
		<-releaseLoad
		return nil, context.DeadlineExceeded
	})
	defer registry.Close()

	initiatorCtx, cancelInitiator := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelInitiator()
	initiatorResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(initiatorCtx, "provider-a")
		initiatorResult <- err
	}()
	<-loadStarted

	waiterResult := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		waiterResult <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(releaseLoad)

	assert.ErrorIs(t, <-initiatorResult, context.DeadlineExceeded)
	assert.ErrorIs(t, <-waiterResult, context.DeadlineExceeded)
	loadMu.Lock()
	assert.Equal(t, 1, loadCount)
	loadMu.Unlock()
}

func TestProviderFinishingAfterRegistryCloseIsClosed(t *testing.T) {
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	provider := &signalCloseProvider{closed: make(chan struct{})}
	registry := newServerRegistry(characterizationConfigs(), func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		close(loadStarted)
		<-releaseLoad
		return provider, nil
	})

	result := make(chan error, 1)
	go func() {
		_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
		result <- err
	}()
	<-loadStarted
	registry.Close()
	close(releaseLoad)

	err := <-result
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry closed")
	select {
	case <-provider.closed:
	case <-time.After(time.Second):
		t.Fatal("provider that finished after registry shutdown was not closed")
	}
}

func TestSameProviderCallsAreSerialized(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	tracker := newConcurrencyTracker()
	registry := newServerRegistry(characterizationConfigs(), recordingFactory(&lifecycleRecorder{}, tracker.handler))

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", nil)
			assert.NoError(t, err)
		}()
	}

	require.Eventually(t, func() bool { return tracker.enteredCount() == 1 }, time.Second, time.Millisecond)
	assert.Equal(t, 1, tracker.maxConcurrency())
	tracker.release()
	wg.Wait()
	assert.Equal(t, 1, tracker.maxConcurrency())
	registry.Close()
}

func TestDifferentProviderCallsRunConcurrently(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	tracker := newConcurrencyTracker()
	registry := newServerRegistry(characterizationConfigs(), recordingFactory(&lifecycleRecorder{}, tracker.handler))

	paths := []string{"alpha.nested.public_tool", "alpha.other_tool"}
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.HandleExecuteTool(context.Background(), registry, path, nil)
			assert.NoError(t, err)
		}()
	}

	require.Eventually(t, func() bool { return tracker.enteredCount() == 2 }, time.Second, time.Millisecond)
	assert.Equal(t, 2, tracker.maxConcurrency())
	tracker.release()
	wg.Wait()
	registry.Close()
}

func TestCanceledCallStopsWaitingForSameProviderLock(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	provider := ProviderClientFunc(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, ctx.Err()
	})
	registry := newServerRegistry(characterizationConfigs(), func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		return provider, nil
	})

	lock := registry.GetClientMutex("provider-a")
	lock.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := h.HandleExecuteTool(ctx, registry, "alpha.nested.public_tool", nil)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Contains(t, err.Error(), "failed to wait for provider provider-a")
	case <-time.After(time.Second):
		t.Fatal("call remained blocked on the provider lock after its deadline")
	}
	lock.Unlock()
}

func TestCanceledContextCannotAcquireAvailableProviderLock(t *testing.T) {
	lock := NewClientMutex()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 100 {
		err := lock.LockContext(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	}

	lock.Lock()
	lock.Unlock()
}

func TestDownstreamErrorsAndStructuredResultsArePreserved(t *testing.T) {
	h := loadCharacterizationHierarchy(t)

	t.Run("JSON-RPC error and schema diagnostic", func(t *testing.T) {
		registry := newServerRegistry(characterizationConfigs(), recordingFactory(&lifecycleRecorder{}, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("downstream rpc failure")
		}))
		defer registry.Close()

		result, err := h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", nil)
		assert.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "downstream rpc failure")
		assert.Contains(t, err.Error(), "Expected inputSchema")
		assert.Contains(t, err.Error(), "\"required\": [\n    \"value\"")
	})

	t.Run("typed JSON-RPC error becomes a faithful tool error", func(t *testing.T) {
		factory := func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
			return ProviderClientFunc(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, &tappetclient.ProviderRPCError{
					Code:    -32042,
					Message: "permission denied",
					Data:    json.RawMessage(`{"account":9007199254740993}`),
				}
			}), nil
		}
		registry := newServerRegistry(characterizationConfigs(), factory)
		defer registry.Close()

		result, err := h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", nil)
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Equal(t, mcp.ResultTypeComplete, result.ResultType)
		assert.JSONEq(t, `{"provider_error":{"code":-32042,"message":"permission denied","data":{"account":9007199254740993}}}`, string(result.RawStructuredContent))
		text, ok := mcp.AsTextContent(result.Content[0])
		require.True(t, ok)
		assert.Contains(t, text.Text, "provider JSON-RPC error -32042: permission denied")
		assert.Contains(t, text.Text, "Expected inputSchema")
	})

	t.Run("isError result keeps structured and non-text content", func(t *testing.T) {
		wantStructured := map[string]interface{}{"code": "invalid_value", "retryable": false}
		registry := newServerRegistry(characterizationConfigs(), recordingFactory(&lifecycleRecorder{}, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError:           true,
				StructuredContent: wantStructured,
				Content: []mcp.Content{
					mcp.NewTextContent("invalid value"),
					mcp.ImageContent{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"},
				},
			}, nil
		}))
		defer registry.Close()

		result, err := h.HandleExecuteTool(context.Background(), registry, "alpha.nested.public_tool", nil)
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Equal(t, wantStructured, result.StructuredContent)
		require.Len(t, result.Content, 2)
		text, ok := mcp.AsTextContent(result.Content[0])
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(text.Text, "invalid value\n\nExpected inputSchema:"))
		_, ok = result.Content[1].(mcp.ImageContent)
		assert.True(t, ok)
	})
}

func TestTimeoutAndCancellationClassification(t *testing.T) {
	h := loadCharacterizationHierarchy(t)
	factory := func(context.Context, string, *config.MCPClientConfigV2) (ProviderClient, error) {
		return ProviderClientFunc(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}), nil
	}

	t.Run("deadline", func(t *testing.T) {
		registry := newServerRegistry(characterizationConfigs(), factory)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := h.HandleExecuteTool(ctx, registry, "alpha.nested.public_tool", nil)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("cancellation", func(t *testing.T) {
		registry := newServerRegistry(characterizationConfigs(), factory)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := h.HandleExecuteTool(ctx, registry, "alpha.nested.public_tool", nil)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

type ProviderClientFunc func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

func (f ProviderClientFunc) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return f(ctx, request)
}

func (ProviderClientFunc) Close() error { return nil }

func (ProviderClientFunc) CloseFailed(context.Context) error { return nil }

type staticProvider struct{}

func (*staticProvider) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("ok"), nil
}

func (*staticProvider) Close() error { return nil }

func (*staticProvider) CloseFailed(context.Context) error { return nil }

type signalCloseProvider struct {
	closed chan struct{}
	once   sync.Once
}

func (*signalCloseProvider) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("ok"), nil
}

func (p *signalCloseProvider) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (p *signalCloseProvider) CloseFailed(context.Context) error { return p.Close() }

type blockingCloseProvider struct {
	closeStarted chan struct{}
	release      chan struct{}
	closeDone    chan struct{}
}

func (*blockingCloseProvider) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return nil, errors.New("unexpected tool call")
}

func (p *blockingCloseProvider) Close() error {
	close(p.closeStarted)
	<-p.release
	close(p.closeDone)
	return nil
}

func (p *blockingCloseProvider) CloseFailed(ctx context.Context) error {
	close(p.closeStarted)
	select {
	case <-p.release:
		close(p.closeDone)
		return nil
	case <-ctx.Done():
		close(p.closeDone)
		return ctx.Err()
	}
}

type lifecycleRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *lifecycleRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *lifecycleRecorder) recordCall(name string) { r.record("call:" + name) }

func (r *lifecycleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type recordingProviderClient struct {
	*mcpclient.Client
	name      string
	recorder  *lifecycleRecorder
	closeOnce sync.Once
}

func (c *recordingProviderClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.recorder.record("close:" + c.name)
		err = c.Client.Close()
	})
	return err
}

func (c *recordingProviderClient) CloseFailed(context.Context) error { return c.Close() }

func recordingFactory(recorder *lifecycleRecorder, handler mcpserver.ToolHandlerFunc) ProviderClientFactory {
	return func(ctx context.Context, name string, _ *config.MCPClientConfigV2) (ProviderClient, error) {
		recorder.record("start:" + name)
		downstream := mcpserver.NewMCPServer(name, "test")
		downstream.AddTool(mcp.NewTool("actual_tool"), handler)
		downstream.AddTool(mcp.NewTool("other_actual_tool"), handler)
		c, err := mcpclient.NewInProcessClient(downstream)
		if err != nil {
			return nil, err
		}
		if err := c.Start(ctx); err != nil {
			return nil, err
		}
		initRequest := mcp.InitializeRequest{}
		initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initRequest.Params.ClientInfo = mcp.Implementation{Name: "tappet-test"}
		if _, err := c.Initialize(ctx, initRequest); err != nil {
			return nil, err
		}
		recorder.record("initialize:" + name)
		return &recordingProviderClient{Client: c, name: name, recorder: recorder}, nil
	}
}

type concurrencyTracker struct {
	mu      sync.Mutex
	active  int
	max     int
	entered int
	done    chan struct{}
	once    sync.Once
}

func newConcurrencyTracker() *concurrencyTracker {
	return &concurrencyTracker{done: make(chan struct{})}
}

func (t *concurrencyTracker) handler(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	t.mu.Lock()
	t.active++
	t.entered++
	if t.active > t.max {
		t.max = t.active
	}
	t.mu.Unlock()
	<-t.done
	t.mu.Lock()
	t.active--
	t.mu.Unlock()
	return mcp.NewToolResultText("ok"), nil
}

func (t *concurrencyTracker) enteredCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.entered
}

func (t *concurrencyTracker) maxConcurrency() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

func (t *concurrencyTracker) release() { t.once.Do(func() { close(t.done) }) }

func characterizationConfigs() map[string]*config.MCPClientConfigV2 {
	return map[string]*config.MCPClientConfigV2{
		"provider-a": {Options: &config.OptionsV2{}},
		"provider-b": {Options: &config.OptionsV2{}},
	}
}

func loadCharacterizationHierarchy(t *testing.T) *Hierarchy {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"root.json":                     `{"overview":"root"}`,
		"alpha/alpha.json":              `{"overview":"alpha"}`,
		"alpha/nested/nested.json":      `{"overview":"nested"}`,
		"alpha/nested/public_tool.json": `{"tools":{"public_tool":{"description":"public tool","maps_to":"actual_tool","server":"provider-a","inputSchema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}}}`,
		"alpha/other_tool.json":         `{"tools":{"other_tool":{"description":"other tool","maps_to":"other_actual_tool","server":"provider-b"}}}`,
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fullPath := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte(files[path]), 0o644))
	}
	h, err := LoadHierarchy(root)
	require.NoError(t, err)
	return h
}
