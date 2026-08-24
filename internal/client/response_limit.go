package client

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
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
	base       http.RoundTripper
	maxBytes   int64
	validation *responseValidation
}

func (t responseLimitedRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		response.Body = newSSEEventLimitedBody(response.Body, t.maxBytes, t.validation)
		return response, nil
	}
	if response.ContentLength > t.maxBytes {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: content length %d exceeds %d bytes", ErrResponseLimitExceeded, response.ContentLength, t.maxBytes)
	}
	response.Body = newResponseLimitedBody(response.Body, t.maxBytes, t.validation)
	return response, nil
}

type sseEventLimitedBody struct {
	body       io.ReadCloser
	reader     *bufio.Reader
	maxBytes   int64
	validation *responseValidation
	pending    []byte
	pendingErr error
}

func newSSEEventLimitedBody(body io.ReadCloser, maxBytes int64, validation *responseValidation) *sseEventLimitedBody {
	return &sseEventLimitedBody{
		body:       body,
		reader:     bufio.NewReader(body),
		maxBytes:   maxBytes,
		validation: validation,
	}
}

func (b *sseEventLimitedBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(b.pending) == 0 {
		if b.pendingErr != nil {
			err := b.pendingErr
			b.pendingErr = nil
			return 0, err
		}
		event, err := b.readEvent()
		if len(event) == 0 {
			return 0, err
		}
		b.pending = event
		b.pendingErr = err
	}

	count := copy(buffer, b.pending)
	b.pending = b.pending[count:]
	return count, nil
}

func (b *sseEventLimitedBody) readEvent() ([]byte, error) {
	event := make([]byte, 0, min(int64(4_096), b.maxBytes))
	atLineStart := true
	for {
		character, err := b.reader.ReadByte()
		if err != nil {
			return event, err
		}
		if int64(len(event)) == b.maxBytes {
			_ = b.body.Close()
			return nil, fmt.Errorf("%w: SSE event exceeds %d bytes", ErrResponseLimitExceeded, b.maxBytes)
		}
		event = append(event, character)

		if character != '\r' && character != '\n' {
			atLineStart = false
			continue
		}

		if character == '\r' {
			if next, peekErr := b.reader.Peek(1); peekErr == nil && next[0] == '\n' {
				if int64(len(event)) == b.maxBytes {
					_ = b.body.Close()
					return nil, fmt.Errorf("%w: SSE event exceeds %d bytes", ErrResponseLimitExceeded, b.maxBytes)
				}
				_, _ = b.reader.ReadByte()
				event = append(event, '\n')
			}
		}

		if atLineStart {
			if err := validateSSEEvent(event, b.validation); err != nil {
				_ = b.body.Close()
				return nil, err
			}
			return event, nil
		}
		atLineStart = true
	}
}

func (b *sseEventLimitedBody) Close() error {
	return b.body.Close()
}

func validateSSEEvent(event []byte, validation *responseValidation) error {
	normalized := bytes.ReplaceAll(event, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))
	var dataLines [][]byte
	for _, line := range bytes.Split(normalized, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data:"))
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		dataLines = append(dataLines, data)
	}
	return validateJSONMessage(bytes.Join(dataLines, []byte("\n")), validation)
}

type responseLimitedBody struct {
	body       io.ReadCloser
	maxBytes   int64
	validation *responseValidation
	loaded     bool
	pending    []byte
	pendingErr error
}

func newResponseLimitedBody(body io.ReadCloser, maxBytes int64, validation *responseValidation) *responseLimitedBody {
	return &responseLimitedBody{body: body, maxBytes: maxBytes, validation: validation}
}

func (b *responseLimitedBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if !b.loaded {
		b.load()
	}
	if len(b.pending) > 0 {
		count := copy(buffer, b.pending)
		b.pending = b.pending[count:]
		return count, nil
	}
	if b.pendingErr != nil {
		err := b.pendingErr
		b.pendingErr = nil
		return 0, err
	}
	return 0, io.EOF
}

func (b *responseLimitedBody) load() {
	b.loaded = true
	data, err := io.ReadAll(io.LimitReader(b.body, b.maxBytes+1))
	if err != nil {
		b.pendingErr = err
		return
	}
	if int64(len(data)) > b.maxBytes {
		b.pendingErr = fmt.Errorf("%w: response exceeds %d bytes", ErrResponseLimitExceeded, b.maxBytes)
		_ = b.body.Close()
		return
	}
	if err := validateJSONMessage(data, b.validation); err != nil {
		b.pendingErr = err
		_ = b.body.Close()
		return
	}
	b.pending = data
}

func (b *responseLimitedBody) Close() error {
	return b.body.Close()
}

func newResponseLimitedHTTPClient(base http.RoundTripper, timeout time.Duration, maxBytes int64) *http.Client {
	return newResponseLimitedHTTPClientWithValidation(base, timeout, maxBytes, nil)
}

func newResponseLimitedHTTPClientWithValidation(base http.RoundTripper, timeout time.Duration, maxBytes int64, validation *responseValidation) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport: responseLimitedRoundTripper{base: base, maxBytes: maxBytes, validation: validation},
		Timeout:   timeout,
	}
}

type boundedLineReader struct {
	reader      *bufio.Reader
	maxBytes    int64
	onLimit     func()
	validation  *responseValidation
	pending     []byte
	pendingErr  error
	limitSignal sync.Once
}

func newBoundedLineReader(reader io.Reader, maxBytes int64, onLimit func()) *boundedLineReader {
	return newValidatedBoundedLineReader(reader, maxBytes, onLimit, nil)
}

func newValidatedBoundedLineReader(reader io.Reader, maxBytes int64, onLimit func(), validation *responseValidation) *boundedLineReader {
	return &boundedLineReader{
		reader:     bufio.NewReader(reader),
		maxBytes:   maxBytes,
		onLimit:    onLimit,
		validation: validation,
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
			return r.validateLine(line, nil)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return r.validateLine(line, err)
		}
	}
}

func (r *boundedLineReader) validateLine(line []byte, readErr error) ([]byte, error) {
	if len(line) == 0 {
		return line, readErr
	}
	if err := validateJSONMessage(line, r.validation); err != nil {
		return nil, err
	}
	return line, readErr
}

type limitedStdioTransport struct {
	command  string
	env      []string
	args     []string
	maxBytes int64

	mu         sync.Mutex
	started    bool
	closed     bool
	inner      *transport.Stdio
	commandIO  *exec.Cmd
	limitCh    chan struct{}
	limitOnce  sync.Once
	validation *responseValidation

	notificationHandler func(mcp.JSONRPCNotification)
	requestHandler      transport.RequestHandler
}

func newLimitedStdioTransport(command string, env, args []string, maxBytes int64) *limitedStdioTransport {
	return &limitedStdioTransport{
		command:    command,
		env:        append([]string(nil), env...),
		args:       append([]string(nil), args...),
		maxBytes:   maxBytes,
		limitCh:    make(chan struct{}),
		validation: newResponseValidation(),
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
	limitedStdout := newValidatedBoundedLineReader(stdout, t.maxBytes, t.signalLimit, t.validation)
	t.inner = transport.NewIO(limitedStdout, stdin, stderr)
	installInboundHandlers(t.inner, t.notificationHandler, t.requestHandler)
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
	responseCh := make(chan limitedStdioResponse, 1)
	go func() {
		value, err := inner.SendRequest(requestCtx, request)
		responseCh <- limitedStdioResponse{value: value, err: err}
	}()

	return awaitLimitedStdioResponse(ctx, t.limitCh, responseCh)
}

type limitedStdioResponse struct {
	value *transport.JSONRPCResponse
	err   error
}

func awaitLimitedStdioResponse(
	ctx context.Context,
	limitCh <-chan struct{},
	responseCh <-chan limitedStdioResponse,
) (*transport.JSONRPCResponse, error) {
	select {
	case <-limitCh:
		return nil, ErrResponseLimitExceeded
	case result := <-responseCh:
		select {
		case <-limitCh:
			return nil, ErrResponseLimitExceeded
		default:
		}
		return result.value, result.err
	case <-ctx.Done():
		select {
		case <-limitCh:
			return nil, ErrResponseLimitExceeded
		default:
		}
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
	t.notificationHandler = handler
	inner := t.inner
	t.mu.Unlock()
	if inner != nil {
		inner.SetNotificationHandler(handler)
	}
}

func (t *limitedStdioTransport) SetRequestHandler(handler transport.RequestHandler) {
	t.mu.Lock()
	t.requestHandler = handler
	inner := t.inner
	t.mu.Unlock()
	if inner != nil {
		inner.SetRequestHandler(handler)
	}
}

type inboundHandlerSetter interface {
	SetNotificationHandler(func(mcp.JSONRPCNotification))
	SetRequestHandler(transport.RequestHandler)
}

func installInboundHandlers(
	target inboundHandlerSetter,
	notificationHandler func(mcp.JSONRPCNotification),
	requestHandler transport.RequestHandler,
) {
	if notificationHandler != nil {
		target.SetNotificationHandler(notificationHandler)
	}
	if requestHandler != nil {
		target.SetRequestHandler(requestHandler)
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

func (t *limitedStdioTransport) CloseFailed(ctx context.Context) error {
	t.mu.Lock()
	t.closed = true
	commandIO := t.commandIO
	t.mu.Unlock()
	if commandIO == nil || commandIO.Process == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	killErr := commandIO.Process.Kill()
	waitErr := commandIO.Wait()
	if killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(killErr, waitErr)
}

func (*limitedStdioTransport) GetSessionId() string {
	return ""
}
