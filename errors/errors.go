// Package errors provides canonical error codes for cloudemu services.
package errors

import "fmt"

// Code represents a canonical error code.
type Code int

const (
	OK Code = iota
	NotFound
	AlreadyExists
	InvalidArgument
	FailedPrecondition
	PermissionDenied
	Throttled
	Internal
	Unimplemented
	ResourceExhausted
	Unavailable
)

var codeNames = map[Code]string{
	OK:                 "OK",
	NotFound:           "NotFound",
	AlreadyExists:      "AlreadyExists",
	InvalidArgument:    "InvalidArgument",
	FailedPrecondition: "FailedPrecondition",
	PermissionDenied:   "PermissionDenied",
	Throttled:          "Throttled",
	Internal:           "Internal",
	Unimplemented:      "Unimplemented",
	ResourceExhausted:  "ResourceExhausted",
	Unavailable:        "Unavailable",
}

// String returns the string representation of the error code.
func (c Code) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("Code(%d)", int(c))
}

// Error is a cloudemu error with a code and message.
type Error struct {
	Code    Code
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New creates a new Error with the given code and message.
func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Newf creates a new Error with the given code and formatted message.
func Newf(code Code, format string, args ...interface{}) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// GetCode extracts the error code from an error. Returns Internal for non-cloudemu errors
// and OK for nil errors.
func GetCode(err error) Code {
	if err == nil {
		return OK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return Internal
}

// IsNotFound returns true if the error is a NotFound error.
func IsNotFound(err error) bool {
	return GetCode(err) == NotFound
}

// IsAlreadyExists returns true if the error is an AlreadyExists error.
func IsAlreadyExists(err error) bool {
	return GetCode(err) == AlreadyExists
}

// IsThrottled returns true if the error is a Throttled error.
func IsThrottled(err error) bool {
	return GetCode(err) == Throttled
}

// IsInvalidArgument returns true if the error is an InvalidArgument error.
func IsInvalidArgument(err error) bool {
	return GetCode(err) == InvalidArgument
}

// IsFailedPrecondition returns true if the error is a FailedPrecondition error.
func IsFailedPrecondition(err error) bool {
	return GetCode(err) == FailedPrecondition
}

// IsPermissionDenied returns true if the error is a PermissionDenied error.
func IsPermissionDenied(err error) bool {
	return GetCode(err) == PermissionDenied
}
