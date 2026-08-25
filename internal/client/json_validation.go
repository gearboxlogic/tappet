package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrDuplicateJSONMember classifies downstream JSON that cannot be decoded
// losslessly because an object contains the same decoded member name twice.
var ErrDuplicateJSONMember = errors.New("duplicate JSON object member")

var (
	ErrJSONDepthExceeded = errors.New("provider message JSON depth exceeded")
	ErrJSONNodesExceeded = errors.New("provider message JSON node count exceeded")
)

// ProviderRPCError preserves a downstream JSON-RPC error, including its
// numeric code and the original JSON bytes of its optional data member.
type ProviderRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ProviderRPCError) Error() string {
	return fmt.Sprintf("provider JSON-RPC error %d: %s", e.Code, e.Message)
}

const (
	maxProviderJSONDepth = 128
	maxProviderJSONNodes = 1_048_576
)

// RejectDuplicateJSONMembers validates one complete JSON value before ordinary
// struct or map decoding can collapse duplicate object members.
func RejectDuplicateJSONMembers(data []byte) error {
	return rejectJSONMembersWithLimits(data, maxProviderJSONDepth, maxProviderJSONNodes)
}

func rejectJSONMembersWithLimits(data []byte, maxDepth, maxNodes int) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	state := jsonScanState{maxDepth: maxDepth, maxNodes: maxNodes}
	if err := scanJSONValue(decoder, &state, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type jsonScanState struct {
	maxDepth int
	maxNodes int
	nodes    int
}

func (s *jsonScanState) addNode() error {
	s.nodes++
	if s.nodes > s.maxNodes {
		return fmt.Errorf("%w: maximum %d", ErrJSONNodesExceeded, s.maxNodes)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, state *jsonScanState, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return state.addNode()
	}
	if depth > state.maxDepth {
		return fmt.Errorf("%w: maximum %d", ErrJSONDepthExceeded, state.maxDepth)
	}
	if err := state.addNode(); err != nil {
		return err
	}

	switch delimiter {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object member name is not a string")
			}
			if err := state.addNode(); err != nil {
				return err
			}
			if _, exists := members[key]; exists {
				return fmt.Errorf("%w %q", ErrDuplicateJSONMember, key)
			}
			members[key] = struct{}{}
			if err := scanJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}

	closingToken, err := decoder.Token()
	if err != nil {
		return err
	}
	closingDelimiter, ok := closingToken.(json.Delim)
	if !ok || (delimiter == '{' && closingDelimiter != '}') || (delimiter == '[' && closingDelimiter != ']') {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}

type responseValidation struct {
	done chan struct{}
	once sync.Once
	err  error

	rpcErrorMu sync.Mutex
	rpcErrors  map[string]*ProviderRPCError
}

func newResponseValidation() *responseValidation {
	return &responseValidation{
		done:      make(chan struct{}),
		rpcErrors: make(map[string]*ProviderRPCError),
	}
}

func (v *responseValidation) fail(err error) {
	if v == nil || err == nil {
		return
	}
	v.once.Do(func() {
		v.err = err
		close(v.done)
	})
}

func validateJSONMessage(data []byte, validation *responseValidation) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil
	}
	if err := RejectDuplicateJSONMembers(trimmed); err != nil {
		validation.fail(err)
		return err
	}
	validation.recordRPCError(trimmed)
	return nil
}

func (v *responseValidation) recordRPCError(data []byte) {
	if v == nil {
		return
	}
	var envelope struct {
		ID    json.RawMessage `json:"id"`
		Error *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error == nil || len(envelope.ID) == 0 {
		return
	}
	key, err := canonicalJSON(envelope.ID)
	if err != nil {
		return
	}
	rpcErr := &ProviderRPCError{
		Code:    envelope.Error.Code,
		Message: envelope.Error.Message,
		Data:    append(json.RawMessage(nil), envelope.Error.Data...),
	}
	v.rpcErrorMu.Lock()
	v.rpcErrors[key] = rpcErr
	v.rpcErrorMu.Unlock()
}

func (v *responseValidation) takeRPCError(id any) *ProviderRPCError {
	if v == nil {
		return nil
	}
	encoded, err := json.Marshal(id)
	if err != nil {
		return nil
	}
	key, err := canonicalJSON(encoded)
	if err != nil {
		return nil
	}
	v.rpcErrorMu.Lock()
	defer v.rpcErrorMu.Unlock()
	rpcErr := v.rpcErrors[key]
	delete(v.rpcErrors, key)
	return rpcErr
}

func canonicalJSON(data []byte) (string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return "", err
	}
	return compact.String(), nil
}
