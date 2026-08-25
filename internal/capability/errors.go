package capability

import "fmt"

// Error classifies a package failure without exposing package contents.
type Error struct {
	Code string
	Path string
	Err  error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s at %s: %v", e.Code, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func packageError(code, path string, err error) error {
	return &Error{Code: code, Path: path, Err: err}
}
