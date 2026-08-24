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
}

func newResponseValidation() *responseValidation {
	return &responseValidation{done: make(chan struct{})}
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
	return nil
}
