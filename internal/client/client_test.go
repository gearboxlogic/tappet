package client

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gearboxlogic/tappet/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailedStdioCleanupKillsAndReapsProvider(t *testing.T) {
	if os.Getenv("TAPPET_FAILED_CLOSE_FIXTURE") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}

	provider, err := NewMCPClient("failed-close", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStdio,
		Command:       os.Args[0],
		Args:          []string{"-test.run=^TestFailedStdioCleanupKillsAndReapsProvider$"},
		Env:           map[string]string{"TAPPET_FAILED_CLOSE_FIXTURE": "1"},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, provider.GetClient().Start(ctx))
	require.NoError(t, provider.CloseFailed(ctx))
}

func TestStdioClientRequiresManualStart(t *testing.T) {
	provider, err := NewMCPClient("deferred-stdio", &config.MCPClientConfigV2{
		TransportType: config.MCPClientTypeStdio,
		Command:       "command-that-does-not-exist",
	})

	require.NoError(t, err)
	assert.True(t, provider.NeedManualStart())
	require.NoError(t, provider.CloseFailed(context.Background()))
}

func TestPingTaskContinuesAfterProviderLocalTimeout(t *testing.T) {
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	defer cancelLifecycle()

	var calls atomic.Int32
	var sawBoundedContext atomic.Bool
	secondCall := make(chan struct{})
	done := make(chan struct{})
	testClient := &Client{name: "ping-fixture"}
	go func() {
		defer close(done)
		testClient.runPingTask(lifecycleCtx, time.Millisecond, 100*time.Millisecond, func(pingCtx context.Context) error {
			if _, ok := pingCtx.Deadline(); ok {
				sawBoundedContext.Store(true)
			}
			switch calls.Add(1) {
			case 1:
				return context.DeadlineExceeded
			case 2:
				close(secondCall)
				return nil
			default:
				return nil
			}
		})
	}()

	select {
	case <-secondCall:
	case <-time.After(time.Second):
		t.Fatal("ping loop stopped after provider-local timeout")
	}
	cancelLifecycle()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ping loop did not stop after lifecycle cancellation")
	}

	require.GreaterOrEqual(t, calls.Load(), int32(2))
	assert.True(t, sawBoundedContext.Load())
}
