package lambda

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestUpdateFunctionMemoryLimits covers UpdateFunction enforcing the same
// MemorySize range (128-10240 MB) CreateFunction does: the boundary values are
// accepted, one step outside either boundary is rejected with InvalidArgument,
// and a rejected update leaves the function's prior MemorySize untouched.
func TestUpdateFunctionMemoryLimits(t *testing.T) {
	tests := []struct {
		name      string
		memory    int
		expectErr bool
	}{
		{name: "minimum boundary accepted", memory: 128},
		{name: "maximum boundary accepted", memory: 10240},
		{name: "one below minimum rejected", memory: 127, expectErr: true},
		{name: "one above maximum rejected", memory: 10241, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			requireNoError(t, mustCreateDefault(m))

			info, err := m.UpdateFunction(context.Background(), "my-func", driver.FunctionConfig{Memory: tc.memory})
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				if !cerrors.IsInvalidArgument(err) {
					t.Fatalf("UpdateFunction(Memory=%d) error code = %v, want InvalidArgument", tc.memory, err)
				}

				got, getErr := m.GetFunction(context.Background(), "my-func")
				requireNoError(t, getErr)
				assertEqual(t, 128, got.Memory) // defaultFuncConfig's original MemorySize

				return
			}

			assertEqual(t, tc.memory, info.Memory)
		})
	}
}

// TestUpdateFunctionTimeoutLimits mirrors TestUpdateFunctionMemoryLimits for
// the Timeout range (1-900 seconds).
func TestUpdateFunctionTimeoutLimits(t *testing.T) {
	tests := []struct {
		name      string
		timeout   int
		expectErr bool
	}{
		{name: "minimum boundary accepted", timeout: 1},
		{name: "maximum boundary accepted", timeout: 900},
		{name: "zero treated as omitted, not rejected", timeout: 0},
		{name: "one above maximum rejected", timeout: 901, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			requireNoError(t, mustCreateDefault(m))

			info, err := m.UpdateFunction(context.Background(), "my-func", driver.FunctionConfig{Timeout: tc.timeout})
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				if !cerrors.IsInvalidArgument(err) {
					t.Fatalf("UpdateFunction(Timeout=%d) error code = %v, want InvalidArgument", tc.timeout, err)
				}

				got, getErr := m.GetFunction(context.Background(), "my-func")
				requireNoError(t, getErr)
				assertEqual(t, 30, got.Timeout) // defaultFuncConfig's original Timeout

				return
			}

			if tc.timeout == 0 {
				// Timeout: 0 in the update request means "field omitted" (the
				// driver has no separate presence flag), so it keeps the
				// function's existing Timeout rather than being applied/rejected.
				assertEqual(t, 30, info.Timeout)

				return
			}

			assertEqual(t, tc.timeout, info.Timeout)
		})
	}
}

// TestUpdateFunctionOmittedFieldsSucceed covers the common Terraform/CLI case
// of an update that only touches one field (e.g. just Description): omitting
// Memory/Timeout/Runtime must not be treated as an invalid 0/"" value — the
// update succeeds and the function keeps its prior Memory/Timeout/Runtime.
func TestUpdateFunctionOmittedFieldsSucceed(t *testing.T) {
	m := newTestMock()
	requireNoError(t, mustCreateDefault(m))

	info, err := m.UpdateFunction(context.Background(), "my-func", driver.FunctionConfig{Description: "only this changed"})
	requireNoError(t, err)
	assertEqual(t, "only this changed", info.Description)
	assertEqual(t, 128, info.Memory)
	assertEqual(t, 30, info.Timeout)
	assertEqual(t, "go1.x", info.Runtime)
}
