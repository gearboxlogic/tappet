package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// mcp-go v1.0.0-beta.1 classifies a missing modern clientCapabilities meta
// member as -32021. SEP-2575 requires malformed per-request metadata to be
// rejected as -32602; -32021 is reserved for an operation that requires a
// capability the client validly omitted. Keep this narrow correction at the
// transport boundary until the SDK ships the same behavior.
func modernMetadataError(message []byte) (json.RawMessage, string, bool) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return nil, "", false
	}
	versionRaw := envelope.Params.Meta[mcp.MetaKeyProtocolVersion]
	var version string
	if json.Unmarshal(versionRaw, &version) != nil || !mcp.IsModernProtocol(version) {
		return nil, "", false
	}
	capabilitiesRaw, present := envelope.Params.Meta[mcp.MetaKeyClientCapabilities]
	if present {
		var capabilities map[string]json.RawMessage
		if json.Unmarshal(capabilitiesRaw, &capabilities) == nil && capabilities != nil {
			return nil, "", false
		}
	}
	return envelope.ID,
		fmt.Sprintf("missing or invalid _meta field %s", mcp.MetaKeyClientCapabilities), true
}

func modernMetadataErrorResponse(id json.RawMessage, message string) []byte {
	response := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: mcp.JSONRPC_VERSION, ID: id}
	if len(response.ID) == 0 {
		response.ID = json.RawMessage("null")
	}
	response.Error.Code = mcp.INVALID_PARAMS
	response.Error.Message = message
	encoded, _ := json.Marshal(response)
	return encoded
}

func modernMetadataValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		id, message, invalid := modernMetadataError(body)
		if !invalid {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(mcp.HeaderProtocolVersion, mcp.LATEST_PROTOCOL_VERSION)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(modernMetadataErrorResponse(id, message))
	})
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *synchronizedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

// protocolValidationReader removes only malformed modern requests affected by
// the SDK classification bug. It writes their JSON-RPC errors itself and
// forwards every other stdio frame unchanged to mcp-go.
type protocolValidationReader struct {
	reader     *bufio.Reader
	writer     io.Writer
	pending    []byte
	pendingErr error
}

func newProtocolValidationReader(reader io.Reader, writer io.Writer) *protocolValidationReader {
	return &protocolValidationReader{reader: bufio.NewReader(reader), writer: writer}
}

func (r *protocolValidationReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		count := copy(buffer, r.pending)
		r.pending = r.pending[count:]
		return count, nil
	}
	if r.pendingErr != nil {
		err := r.pendingErr
		r.pendingErr = nil
		return 0, err
	}
	for {
		line, err := r.reader.ReadBytes('\n')
		if len(line) == 0 {
			return 0, err
		}
		id, message, invalid := modernMetadataError(bytes.TrimSpace(line))
		if !invalid {
			r.pending = line
			r.pendingErr = err
			count := copy(buffer, r.pending)
			r.pending = r.pending[count:]
			return count, nil
		}
		response := append(modernMetadataErrorResponse(id, message), '\n')
		if _, writeErr := r.writer.Write(response); writeErr != nil {
			return 0, writeErr
		}
		if err != nil {
			return 0, err
		}
	}
}
