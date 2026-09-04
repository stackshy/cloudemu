package lambda

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestValidateRuntime covers validateRuntime directly: known-good runtimes
// (including ones added after the initial validRuntimes snapshot, to guard
// against re-introducing over-rejection), a garbage value real users never
// legitimately send, and a runtime shaped like a real identifier but not yet
// in the explicit snapshot — accepted per the family-pattern fallback so a
// brand-new AWS runtime release never gets wrongly rejected before this list
// is updated.
func TestValidateRuntime(t *testing.T) {
	tests := []struct {
		name      string
		runtime   string
		expectErr bool
	}{
		{name: "empty allowed (container image)", runtime: ""},
		{name: "current nodejs22.x", runtime: "nodejs22.x"},
		{name: "current nodejs24.x", runtime: "nodejs24.x"},
		{name: "current python3.13", runtime: "python3.13"},
		{name: "unlisted but well-formed nodejs99.x accepted", runtime: "nodejs99.x"},
		{name: "unlisted but well-formed python4.0 accepted", runtime: "python4.0"},
		{name: "unlisted but well-formed provided.al2099 accepted", runtime: "provided.al2099"},
		{name: "garbage rejected", runtime: "not-a-runtime", expectErr: true},
		{name: "single-char garbage rejected", runtime: "x", expectErr: true},
		{name: "empty-ish garbage rejected", runtime: "totally-fake-runtime", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRuntime(tc.runtime)
			assertError(t, err, tc.expectErr)

			if tc.expectErr && !cerrors.IsInvalidArgument(err) {
				t.Fatalf("validateRuntime(%q) error code = %v, want InvalidArgument", tc.runtime, err)
			}
		})
	}
}

// TestCreateFunctionRuntimeValidation covers CreateFunction rejecting a
// garbage Runtime and accepting both a current-snapshot and an unlisted-but-
// well-formed one.
func TestCreateFunctionRuntimeValidation(t *testing.T) {
	tests := []struct {
		name      string
		runtime   string
		expectErr bool
	}{
		{name: "current runtime accepted", runtime: "nodejs24.x"},
		{name: "unlisted well-formed runtime accepted", runtime: "nodejs99.x"},
		{name: "garbage runtime rejected", runtime: "not-a-runtime", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			cfg := defaultFuncConfig()
			cfg.Runtime = tc.runtime

			info, err := m.CreateFunction(context.Background(), cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				if !cerrors.IsInvalidArgument(err) {
					t.Fatalf("CreateFunction(Runtime=%q) error code = %v, want InvalidArgument", tc.runtime, err)
				}

				return
			}

			assertEqual(t, tc.runtime, info.Runtime)
		})
	}
}

// TestUpdateFunctionRuntimeValidation mirrors TestCreateFunctionRuntimeValidation
// for UpdateFunctionConfiguration (UpdateFunction here), and additionally covers
// an update that omits Runtime entirely: it must succeed and keep the
// function's existing Runtime rather than being rejected.
func TestUpdateFunctionRuntimeValidation(t *testing.T) {
	tests := []struct {
		name      string
		runtime   string
		expectErr bool
	}{
		{name: "current runtime accepted", runtime: "python3.13"},
		{name: "unlisted well-formed runtime accepted", runtime: "python4.0"},
		{name: "garbage runtime rejected", runtime: "not-a-runtime", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			requireNoError(t, mustCreateDefault(m))

			info, err := m.UpdateFunction(context.Background(), "my-func", driver.FunctionConfig{Runtime: tc.runtime})
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				if !cerrors.IsInvalidArgument(err) {
					t.Fatalf("UpdateFunction(Runtime=%q) error code = %v, want InvalidArgument", tc.runtime, err)
				}

				// A rejected update must not have touched the function's Runtime.
				got, getErr := m.GetFunction(context.Background(), "my-func")
				requireNoError(t, getErr)
				assertEqual(t, "go1.x", got.Runtime)

				return
			}

			assertEqual(t, tc.runtime, info.Runtime)
		})
	}

	t.Run("update omitting runtime keeps prior value", func(t *testing.T) {
		m := newTestMock()
		requireNoError(t, mustCreateDefault(m))

		info, err := m.UpdateFunction(context.Background(), "my-func", driver.FunctionConfig{Description: "new desc"})
		requireNoError(t, err)
		assertEqual(t, "go1.x", info.Runtime)
	})
}

// mustCreateDefault creates the standard defaultFuncConfig function and
// returns any error, for tests that only need the function to exist.
func mustCreateDefault(m *Mock) error {
	_, err := m.CreateFunction(context.Background(), defaultFuncConfig())

	return err
}
