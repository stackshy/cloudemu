package lambda

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

func TestCreateFunctionEnvironmentValidation(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		expectErr bool
	}{
		{name: "plain keys accepted", env: map[string]string{"STAGE": "dev", "TZ": "UTC"}},
		{name: "AWS_REGION reserved", env: map[string]string{"AWS_REGION": "us-west-2"}, expectErr: true},
		{name: "_HANDLER reserved", env: map[string]string{"_HANDLER": "x"}, expectErr: true},
		{name: "session token reserved", env: map[string]string{"AWS_SESSION_TOKEN": "x"}, expectErr: true},
		{name: "over 4 KB rejected", env: map[string]string{"BIG": strings.Repeat("a", 4096)}, expectErr: true},
		{name: "just under 4 KB accepted", env: map[string]string{"BIG": strings.Repeat("a", 4096-len(`{"BIG":""}`))}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			cfg := defaultFuncConfig()
			cfg.Environment = tc.env

			_, err := m.CreateFunction(context.Background(), cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr && !cerrors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestUpdateFunctionRejectsReservedEnvironment(t *testing.T) {
	m := newTestMock()
	requireNoError(t, mustCreateDefault(m))

	_, err := m.UpdateFunction(context.Background(), "my-func",
		driver.FunctionConfig{Environment: map[string]string{"AWS_LAMBDA_FUNCTION_NAME": "x"}})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}

	got, err := m.GetFunction(context.Background(), "my-func")
	requireNoError(t, err)

	if _, ok := got.Environment["AWS_LAMBDA_FUNCTION_NAME"]; ok {
		t.Fatal("rejected update was applied")
	}
}
