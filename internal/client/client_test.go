package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
