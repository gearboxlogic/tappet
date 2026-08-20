package hierarchy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gearboxlogic/capscope/internal/config"
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

	assert.Empty(t, recorder.snapshot(), "loading CapScope must not start a provider")
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

func TestSSEProviderOutlivesTriggeringRequest(t *testing.T) {
	downstream := mcpserver.NewMCPServer("sse-provider", "test")
	downstream.AddTool(mcp.NewTool("actual_tool"), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	testServer := mcpserver.NewTestServer(downstream)
	t.Cleanup(testServer.Close)

	configs := characterizationConfigs()
	configs["provider-a"] = &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeSSE,
		URL:           testServer.URL + "/sse",
	}
	registry := NewServerRegistry(configs)
	defer registry.Close()
	h := loadCharacterizationHierarchy(t)

	triggerCtx, cancelTrigger := context.WithCancel(context.Background())
	result, err := h.HandleExecuteTool(triggerCtx, registry, "alpha.nested.public_tool", nil)
	require.NoError(t, err)
	require.False(t, result.IsError)
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
			closeFailedProviderAsync(name, failedProvider)
			return nil, initializeErr
		}
		return ProviderClientFunc(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		}), nil
	})
	defer registry.Close()

	_, err := registry.GetOrLoadServer(context.Background(), "provider-a")
	assert.ErrorIs(t, err, initializeErr)
	require.Eventually(t, func() bool {
		select {
		case <-closeStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

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
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("failed-provider cleanup did not finish after release")
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

func TestCanceledCallWaitsForSameProviderLockThenPreservesClassification(t *testing.T) {
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

	<-ctx.Done()
	select {
	case err := <-errCh:
		t.Fatalf("call returned before the inherited mutex was released: %v", err)
	default:
	}
	lock.Unlock()
	err := <-errCh
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "failed to call tool actual_tool")
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
		initRequest.Params.ClientInfo = mcp.Implementation{Name: "capscope-test"}
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
