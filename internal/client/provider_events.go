package client

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// ErrProviderEventOverflow classifies a provider connection closed because
// its notification work exceeded the bounded event queue.
var ErrProviderEventOverflow = errors.New("provider event queue overflow")

const (
	maxProviderQueuedEvents     = 256
	maxProviderQueuedEventBytes = 16 << 20
	maxProviderEventHandlers    = 16
)

type queuedProviderEvent struct {
	notification mcp.JSONRPCNotification
	bytes        int
}

// providerEventQueue keeps decoded notification work bounded. The raw frame
// has already passed the byte, depth, node, and duplicate-key scan before it
// reaches this queue.
type providerEventQueue struct {
	validation *responseValidation
	overflow   func()
	queue      chan queuedProviderEvent
	done       chan struct{}

	startOnce    sync.Once
	stopOnce     sync.Once
	overflowOnce sync.Once

	mu          sync.Mutex
	queuedBytes int
	handler     func(mcp.JSONRPCNotification)
}

func newProviderEventQueue(validation *responseValidation, overflow func()) *providerEventQueue {
	return &providerEventQueue{
		validation: validation,
		overflow:   overflow,
		queue:      make(chan queuedProviderEvent, maxProviderQueuedEvents),
		done:       make(chan struct{}),
	}
}

func (q *providerEventQueue) start() {
	q.startOnce.Do(func() {
		for range maxProviderEventHandlers {
			go q.run()
		}
	})
}

func (q *providerEventQueue) stop() {
	q.stopOnce.Do(func() { close(q.done) })
}

func (q *providerEventQueue) setHandler(handler func(mcp.JSONRPCNotification)) {
	q.mu.Lock()
	q.handler = handler
	q.mu.Unlock()
}

func (q *providerEventQueue) enqueue(notification mcp.JSONRPCNotification) {
	encoded, err := json.Marshal(notification)
	if err != nil {
		q.fail(err)
		return
	}
	event := queuedProviderEvent{notification: notification, bytes: len(encoded)}

	q.mu.Lock()
	if event.bytes > maxProviderQueuedEventBytes ||
		q.queuedBytes > maxProviderQueuedEventBytes-event.bytes {
		q.mu.Unlock()
		q.fail(ErrProviderEventOverflow)
		return
	}
	select {
	case q.queue <- event:
		q.queuedBytes += event.bytes
		q.mu.Unlock()
	case <-q.done:
		q.mu.Unlock()
	default:
		q.mu.Unlock()
		q.fail(ErrProviderEventOverflow)
	}
}

func (q *providerEventQueue) run() {
	for {
		select {
		case <-q.done:
			return
		case event := <-q.queue:
			q.mu.Lock()
			q.queuedBytes -= event.bytes
			handler := q.handler
			q.mu.Unlock()
			if handler != nil {
				handler(event.notification)
			}
		}
	}
}

func (q *providerEventQueue) fail(err error) {
	if !errors.Is(err, ErrProviderEventOverflow) {
		err = errors.Join(ErrProviderEventOverflow, err)
	}
	q.validation.fail(err)
	q.overflowOnce.Do(func() {
		q.stop()
		// Closing a transport from its reader callback can deadlock in some SDK
		// implementations. One bounded connection-level goroutine performs it.
		go q.overflow()
	})
}
