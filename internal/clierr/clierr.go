// Package clierr defines stable machine-readable CLI error classes and exit
// codes. Commands may leave errors unwrapped; Execute then reports the generic
// class.
package clierr

import "errors"

const (
	CodeGeneric     = "error"
	CodeValidation  = "validation"
	CodeNotFound    = "not_found"
	CodeAuth        = "auth"
	CodeTimeout     = "timeout"
	CodeConflict    = "conflict"
	CodeUnsupported = "unsupported"
)

const (
	ExitGeneric     = 1
	ExitValidation  = 2
	ExitNotFound    = 3
	ExitAuth        = 4
	ExitTimeout     = 5
	ExitConflict    = 6
	ExitUnsupported = 7
)

// Error is a classified CLI error.
type Error struct {
	Code string
	Hint string
	Exit int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func exitFor(code string) int {
	switch code {
	case CodeValidation:
		return ExitValidation
	case CodeNotFound:
		return ExitNotFound
	case CodeAuth:
		return ExitAuth
	case CodeTimeout:
		return ExitTimeout
	case CodeConflict:
		return ExitConflict
	case CodeUnsupported:
		return ExitUnsupported
	default:
		return ExitGeneric
	}
}

// New builds a classified error.
func New(code, message, hint string) error {
	return &Error{Code: code, Hint: hint, Exit: exitFor(code), Err: errors.New(message)}
}

// Wrap classifies an existing error.
func Wrap(code string, err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}
	return &Error{Code: code, Exit: exitFor(code), Err: err}
}

// WithHint adds a remediation hint while preserving the class.
func WithHint(err error, hint string) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		out := *existing
		out.Hint = hint
		return &out
	}
	return &Error{Code: CodeGeneric, Hint: hint, Exit: ExitGeneric, Err: err}
}

// Info extracts the stable code, message, hint and exit code.
func Info(err error) (code, message, hint string, exit int) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Err.Error(), e.Hint, e.Exit
	}
	return CodeGeneric, err.Error(), "", ExitGeneric
}
