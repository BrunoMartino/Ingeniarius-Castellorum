package guard

import (
	"errors"
	"fmt"
	"strings"
)

// Structured guardrail codes. Every refusal carries one of these.
const (
	CodeDeniedDelete = "DENIED_DELETE"
	CodeDeniedOnAir  = "DENIED_ONAIR"
	CodeDeniedCLI    = "DENIED_CLI"
	CodeDeniedScope  = "DENIED_SCOPE"
	CodeUpstream     = "UPSTREAM_ERROR"
	CodeNotFound     = "NOT_FOUND"
	CodeBadInput     = "BAD_INPUT"
	CodeConfig       = "CONFIG_ERROR"
)

// Error is a refusal or failure with a stable code, a reason and — when the
// caller has a way forward — the remedy to follow. Never carries secrets.
type Error struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}

func NewError(code, reason string) Error {
	return Error{Code: code, Reason: reason}
}

func NewErrorWithRemedy(code, reason, remedy string) Error {
	return Error{Code: code, Reason: reason, Remedy: remedy}
}

func (e Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Code)
	if e.Reason != "" {
		b.WriteString(": ")
		b.WriteString(e.Reason)
	}
	if e.Remedy != "" {
		b.WriteString(" | remedy: ")
		b.WriteString(e.Remedy)
	}
	return b.String()
}

// Code returns the guardrail code of err, or "" when err is not a guard.Error.
func Code(err error) string {
	var ge Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	return ""
}

func IsCode(err error, code string) bool {
	return err != nil && Code(err) == code
}

func badInput(format string, args ...any) Error {
	return NewError(CodeBadInput, fmt.Sprintf(format, args...))
}
