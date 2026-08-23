package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ErrResponseLimitExceeded classifies a downstream frame rejected before JSON
// decoding because it exceeded the configured transport limit.
var ErrResponseLimitExceeded = errors.New("downstream response exceeds configured byte limit")

type responseLimitedRoundTripper struct {
	base     http.RoundTripper
	maxBytes int64
}

func (t responseLimitedRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > t.maxBytes {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: content length %d exceeds %d bytes", ErrResponseLimitExceeded, response.ContentLength, t.maxBytes)
	}
	response.Body = &responseLimitedBody{
		body:      response.Body,
		remaining: t.maxBytes,
		maxBytes:  t.maxBytes,
	}
	return response, nil
}

type responseLimitedBody struct {
	body      io.ReadCloser
	remaining int64
	maxBytes  int64
}

func (b *responseLimitedBody) Read(buffer []byte) (int, error) {
	if b.remaining == 0 {
		var extra [1]byte
		count, err := b.body.Read(extra[:])
		if count > 0 {
			_ = b.body.Close()
			return 0, fmt.Errorf("%w: response exceeds %d bytes", ErrResponseLimitExceeded, b.maxBytes)
		}
		return 0, err
	}
	if int64(len(buffer)) > b.remaining {
		buffer = buffer[:b.remaining]
	}
	count, err := b.body.Read(buffer)
	b.remaining -= int64(count)
	return count, err
}

func (b *responseLimitedBody) Close() error {
	return b.body.Close()
}

func newResponseLimitedHTTPClient(base http.RoundTripper, timeout time.Duration, maxBytes int64) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport: responseLimitedRoundTripper{base: base, maxBytes: maxBytes},
		Timeout:   timeout,
	}
}

type boundedLineReader struct {
	reader      *bufio.Reader
	maxBytes    int64
	onLimit     func()
	pending     []byte
	pendingErr  error
	limitSignal sync.Once
}

func newBoundedLineReader(reader io.Reader, maxBytes int64, onLimit func()) *boundedLineReader {
	return &boundedLineReader{
		reader:   bufio.NewReader(reader),
		maxBytes: maxBytes,
		onLimit:  onLimit,
	}
}

func (r *boundedLineReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		if r.pendingErr != nil {
			err := r.pendingErr
			r.pendingErr = nil
			return 0, err
		}
		line, err := r.readLine()
		if len(line) == 0 {
			return 0, err
		}
		r.pending = line
		r.pendingErr = err
	}

	count := copy(buffer, r.pending)
	r.pending = r.pending[count:]
	return count, nil
}

func (r *boundedLineReader) readLine() ([]byte, error) {
	line := make([]byte, 0, min(int64(4_096), r.maxBytes))
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if int64(len(line))+int64(len(fragment)) > r.maxBytes {
			r.limitSignal.Do(func() {
				if r.onLimit != nil {
					r.onLimit()
				}
			})
			return nil, fmt.Errorf("%w: stdio frame exceeds %d bytes", ErrResponseLimitExceeded, r.maxBytes)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return line, err
		}
	}
}

type limitedStdioTransport struct {
	command  string
	env      []string
	args     []string
	maxBytes int64

	mu        sync.Mutex
	started   bool
	closed    bool
	inner     *transport.Stdio
	commandIO *exec.Cmd
	limitCh   chan struct{}
	limitOnce sync.Once
}

func newLimitedStdioTransport(command string, env, args []string, maxBytes int64) *limitedStdioTransport {
	return &limitedStdioTransport{
		command:  command,
		env:      append([]string(nil), env...),
		args:     append([]string(nil), args...),
		maxBytes: maxBytes,
		limitCh:  make(chan struct{}),
	}
}

func (t *limitedStdioTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return nil
	}
	if t.closed {
		return errors.New("stdio transport is closed")
	}

	commandIO := exec.CommandContext(ctx, t.command, t.args...)
	commandIO.Env = append(os.Environ(), t.env...)
	stdin, err := commandIO.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := commandIO.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := commandIO.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	if err := commandIO.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	t.commandIO = commandIO
	limitedStdout := newBoundedLineReader(stdout, t.maxBytes, t.signalLimit)
	t.inner = transport.NewIO(limitedStdout, stdin, stderr)
	if err := t.inner.Start(ctx); err != nil {
		_ = commandIO.Process.Kill()
		_ = commandIO.Wait()
		return err
	}
	t.started = true
	return nil
}

func (t *limitedStdioTransport) signalLimit() {
	t.limitOnce.Do(func() {
		close(t.limitCh)
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.commandIO != nil && t.commandIO.Process != nil {
			_ = t.commandIO.Process.Kill()
		}
	})
}

func (t *limitedStdioTransport) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	t.mu.Lock()
	inner := t.inner
	t.mu.Unlock()
	if inner == nil {
		return nil, errors.New("stdio transport is not started")
	}
	select {
	case <-t.limitCh:
		return nil, ErrResponseLimitExceeded
	default:
	}

	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type response struct {
		value *transport.JSONRPCResponse
		err   error
	}
	responseCh := make(chan response, 1)
	go func() {
		value, err := inner.SendRequest(requestCtx, request)
		responseCh <- response{value: value, err: err}
	}()

	select {
	case <-t.limitCh:
		return nil, ErrResponseLimitExceeded
	case result := <-responseCh:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *limitedStdioTransport) SendNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	t.mu.Lock()
	inner := t.inner
	t.mu.Unlock()
	if inner == nil {
		return errors.New("stdio transport is not started")
	}
	select {
	case <-t.limitCh:
		return ErrResponseLimitExceeded
	default:
		return inner.SendNotification(ctx, notification)
	}
}

func (t *limitedStdioTransport) SetNotificationHandler(handler func(mcp.JSONRPCNotification)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inner != nil {
		t.inner.SetNotificationHandler(handler)
	}
}

func (t *limitedStdioTransport) SetRequestHandler(handler transport.RequestHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inner != nil {
		t.inner.SetRequestHandler(handler)
	}
}

func (t *limitedStdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	inner := t.inner
	commandIO := t.commandIO
	t.mu.Unlock()

	var closeErr error
	if inner != nil {
		closeErr = inner.Close()
	}
	var waitErr error
	if commandIO != nil {
		waitErr = commandIO.Wait()
	}
	select {
	case <-t.limitCh:
		waitErr = nil
	default:
	}
	return errors.Join(closeErr, waitErr)
}

func (*limitedStdioTransport) GetSessionId() string {
	return ""
}
