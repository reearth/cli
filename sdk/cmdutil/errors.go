package cmdutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/reearth/cli/sdk/prompt"
)

// Exit codes. Keep in sync with sdk/corecmd/topics/exit-codes.txt and sdk/skills/SKILL.md.tmpl.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitAuth     = 4
	ExitNotFound = 5
	ExitCancel   = 8
)

// Error is a user-facing error with a stable machine-readable code and an optional hint.
type Error struct {
	Exit    int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Err     error  `json:"-"`
	// Silent means the command already reported the problem; only the exit code matters.
	Silent bool `json:"-"`
}

// SilentExit returns an error that only sets the exit code.
func SilentExit(exit int, code string) *Error {
	return &Error{Exit: exit, Code: code, Message: code, Silent: true}
}

func (e *Error) Error() string {
	if e.Err != nil && e.Message == "" {
		return e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// NewError creates an *Error.
func NewError(exit int, code, message, hint string) *Error {
	return &Error{Exit: exit, Code: code, Message: message, Hint: hint}
}

// FlagErrorf reports invalid usage (exit code 2).
func FlagErrorf(format string, args ...any) *Error {
	return &Error{Exit: ExitUsage, Code: "usage", Message: fmt.Sprintf(format, args...)}
}

// NotFoundf reports a missing resource (exit code 5).
func NotFoundf(format string, args ...any) *Error {
	return &Error{Exit: ExitNotFound, Code: "not_found", Message: fmt.Sprintf(format, args...)}
}

// ErrCancel is returned when the user aborts an operation.
var ErrCancel = &Error{Exit: ExitCancel, Code: "cancelled", Message: "cancelled"}

// NoInputError is returned when a value is required but prompting is not possible.
func NoInputError(what, flag string) *Error {
	return &Error{
		Exit:    ExitUsage,
		Code:    "input_required",
		Message: what + " is required",
		Hint:    "pass " + flag + " (prompting is disabled in non-interactive mode)",
	}
}

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var e *Error
	if errors.As(err, &e) && e.Exit != 0 {
		return e.Exit
	}
	if errors.Is(err, prompt.ErrCancelled) || errors.Is(err, context.Canceled) {
		return ExitCancel
	}
	if errors.Is(err, prompt.ErrNoInput) {
		return ExitUsage
	}
	return ExitError
}

// AsError converts any error into an *Error for structured reporting.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		if e.Code == "" {
			e.Code = "error"
		}
		if e.Message == "" {
			e.Message = err.Error()
		}
		return e
	}
	switch {
	case errors.Is(err, prompt.ErrCancelled), errors.Is(err, context.Canceled):
		return ErrCancel
	case errors.Is(err, prompt.ErrNoInput):
		return &Error{Exit: ExitUsage, Code: "input_required", Message: err.Error(), Hint: "pass the value as a flag or argument"}
	}
	return &Error{Exit: ExitError, Code: "error", Message: err.Error(), Err: err}
}
