package errors

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCode_String(t *testing.T) {
	tests := []struct {
		name   string
		code   Code
		expect string
	}{
		{name: "OK", code: OK, expect: "OK"},
		{name: "NotFound", code: NotFound, expect: "NotFound"},
		{name: "AlreadyExists", code: AlreadyExists, expect: "AlreadyExists"},
		{name: "InvalidArgument", code: InvalidArgument, expect: "InvalidArgument"},
		{name: "FailedPrecondition", code: FailedPrecondition, expect: "FailedPrecondition"},
		{name: "PermissionDenied", code: PermissionDenied, expect: "PermissionDenied"},
		{name: "Throttled", code: Throttled, expect: "Throttled"},
		{name: "Internal", code: Internal, expect: "Internal"},
		{name: "Unimplemented", code: Unimplemented, expect: "Unimplemented"},
		{name: "ResourceExhausted", code: ResourceExhausted, expect: "ResourceExhausted"},
		{name: "Unavailable", code: Unavailable, expect: "Unavailable"},
		{name: "unknown code", code: Code(999), expect: "Code(999)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, tc.code.String())
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		code    Code
		msg     string
		wantErr string
	}{
		{name: "not found", code: NotFound, msg: "bucket missing", wantErr: "NotFound: bucket missing"},
		{name: "already exists", code: AlreadyExists, msg: "duplicate", wantErr: "AlreadyExists: duplicate"},
		{name: "empty message", code: Internal, msg: "", wantErr: "Internal: "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := New(tc.code, tc.msg)
			assert.Equal(t, tc.code, err.Code)
			assert.Equal(t, tc.msg, err.Message)
			assert.Equal(t, tc.wantErr, err.Error())
		})
	}
}

func TestNewf(t *testing.T) {
	tests := []struct {
		name    string
		code    Code
		format  string
		args    []any
		wantMsg string
	}{
		{name: "formatted message", code: NotFound, format: "resource %s not found", args: []any{"vm-1"}, wantMsg: "resource vm-1 not found"},
		{name: "no args", code: InvalidArgument, format: "bad input", args: nil, wantMsg: "bad input"},
		{name: "numeric arg", code: Throttled, format: "limit %d exceeded", args: []any{100}, wantMsg: "limit 100 exceeded"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Newf(tc.code, tc.format, tc.args...)
			assert.Equal(t, tc.code, err.Code)
			assert.Equal(t, tc.wantMsg, err.Message)
		})
	}
}

func TestGetCode(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect Code
	}{
		{name: "nil error", err: nil, expect: OK},
		{name: "cloudemu error", err: New(NotFound, "gone"), expect: NotFound},
		{name: "standard error", err: fmt.Errorf("oops"), expect: Internal},
		{name: "wrapped cloudemu error", err: fmt.Errorf("wrap: %w", New(AlreadyExists, "dup")), expect: AlreadyExists},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, GetCode(tc.err))
		})
	}
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, IsNotFound(New(NotFound, "x")))
	assert.False(t, IsNotFound(New(Internal, "x")))
	assert.False(t, IsNotFound(nil))
}

func TestIsAlreadyExists(t *testing.T) {
	assert.True(t, IsAlreadyExists(New(AlreadyExists, "x")))
	assert.False(t, IsAlreadyExists(New(NotFound, "x")))
	assert.False(t, IsAlreadyExists(nil))
}

func TestIsThrottled(t *testing.T) {
	assert.True(t, IsThrottled(New(Throttled, "x")))
	assert.False(t, IsThrottled(New(OK, "x")))
	assert.False(t, IsThrottled(nil))
}

func TestIsInvalidArgument(t *testing.T) {
	assert.True(t, IsInvalidArgument(New(InvalidArgument, "x")))
	assert.False(t, IsInvalidArgument(New(NotFound, "x")))
	assert.False(t, IsInvalidArgument(nil))
}

func TestIsFailedPrecondition(t *testing.T) {
	assert.True(t, IsFailedPrecondition(New(FailedPrecondition, "x")))
	assert.False(t, IsFailedPrecondition(New(OK, "x")))
	assert.False(t, IsFailedPrecondition(nil))
}

func TestIsPermissionDenied(t *testing.T) {
	assert.True(t, IsPermissionDenied(New(PermissionDenied, "x")))
	assert.False(t, IsPermissionDenied(New(NotFound, "x")))
	assert.False(t, IsPermissionDenied(nil))
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	var err error = New(NotFound, "test")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "NotFound")
}
