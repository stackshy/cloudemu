package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// describeByName returns the metadata for a single parameter name.
func describeByName(t *testing.T, m interface {
	DescribeParameters(context.Context) ([]driver.ParameterMetadata, error)
}, name string) driver.ParameterMetadata {
	t.Helper()

	metas, err := m.DescribeParameters(context.Background())
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	for _, md := range metas {
		if md.Name == name {
			return md
		}
	}

	t.Fatalf("parameter %q not found in DescribeParameters", name)

	return driver.ParameterMetadata{}
}

func TestPutSecureStringDefaultsKeyID(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/sec", Value: "v", Type: driver.TypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	md := describeByName(t, m, "/sec")
	if md.KeyID != driver.DefaultSecureStringKeyID {
		t.Fatalf("KeyId = %q, want default %q", md.KeyID, driver.DefaultSecureStringKeyID)
	}
}

func TestPutSecureStringExplicitKeyID(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const key = "alias/my-key"

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/sec", Value: "v", Type: driver.TypeSecureString, KeyID: key,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	md := describeByName(t, m, "/sec")
	if md.KeyID != key {
		t.Fatalf("KeyId = %q, want %q", md.KeyID, key)
	}
}

func TestPutKeyIDOnNonSecureRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, typ := range []string{driver.TypeString, driver.TypeStringList} {
		_, _, err := m.PutParameter(ctx, driver.PutConfig{
			Name: "/p-" + typ, Value: "v", Type: typ, KeyID: "alias/my-key",
		})
		if err == nil {
			t.Fatalf("type %s with KeyId: want rejection, got nil", typ)
		}

		if !errors.Is(err, driver.ErrKeyIDOnNonSecure) {
			t.Fatalf("type %s with KeyId: want ErrKeyIDOnNonSecure, got %v", typ, err)
		}
	}
}

// GetParameter (and GetParameters) must NOT expose KeyId — real SSM's Parameter
// shape has no KeyId field; it only appears on DescribeParameters metadata.
func TestGetParameterOmitsKeyID(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/sec", Value: "v", Type: driver.TypeSecureString, KeyID: "alias/my-key",
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	got, err := m.GetParameter(ctx, "/sec", true)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.KeyID != "" {
		t.Fatalf("GetParameter KeyId = %q, want empty (not exposed)", got.KeyID)
	}

	if got.AllowedPattern != "" {
		t.Fatalf("GetParameter AllowedPattern = %q, want empty (not exposed)", got.AllowedPattern)
	}
}

func TestPutAllowedPatternMatchPasses(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "12345", Type: driver.TypeString, AllowedPattern: `^\d+$`,
	}); err != nil {
		t.Fatalf("PutParameter matching pattern: %v", err)
	}

	md := describeByName(t, m, "/p")
	if md.AllowedPattern != `^\d+$` {
		t.Fatalf("AllowedPattern = %q, want %q", md.AllowedPattern, `^\d+$`)
	}
}

func TestPutAllowedPatternMismatchRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "abc", Type: driver.TypeString, AllowedPattern: `^\d+$`,
	})
	if err == nil {
		t.Fatal("value not matching pattern: want rejection, got nil")
	}

	if !errors.Is(err, driver.ErrValuePatternMismatch) {
		t.Fatalf("want ErrValuePatternMismatch, got %v", err)
	}
}

func TestPutAllowedPatternInvalidRegexRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "x", Type: driver.TypeString, AllowedPattern: `[unterminated`,
	})
	if err == nil {
		t.Fatal("invalid regex pattern: want rejection, got nil")
	}

	if !errors.Is(err, driver.ErrInvalidAllowedPattern) {
		t.Fatalf("want ErrInvalidAllowedPattern, got %v", err)
	}
}

func TestPutAllowedPatternEnforcedOnOverwrite(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "1", Type: driver.TypeString, AllowedPattern: `^\d+$`,
	}); err != nil {
		t.Fatalf("PutParameter create: %v", err)
	}

	// Overwrite with a value that violates the pattern must be rejected.
	_, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "abc", Type: driver.TypeString, Overwrite: true, AllowedPattern: `^\d+$`,
	})
	if err == nil {
		t.Fatal("overwrite violating pattern: want rejection, got nil")
	}

	if !errors.Is(err, driver.ErrValuePatternMismatch) {
		t.Fatalf("want ErrValuePatternMismatch on overwrite, got %v", err)
	}

	// A matching overwrite still succeeds.
	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/p", Value: "99", Type: driver.TypeString, Overwrite: true, AllowedPattern: `^\d+$`,
	}); err != nil {
		t.Fatalf("overwrite matching pattern: %v", err)
	}
}

// DescribeParameters reflects both KeyId and AllowedPattern in one metadata entry.
func TestDescribeParametersReflectsKeyIDAndPattern(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const (
		key     = "alias/combo-key"
		pattern = `^[a-z]+$`
	)

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/combo", Value: "abc", Type: driver.TypeSecureString, KeyID: key, AllowedPattern: pattern,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	md := describeByName(t, m, "/combo")
	if md.KeyID != key {
		t.Fatalf("KeyId = %q, want %q", md.KeyID, key)
	}

	if md.AllowedPattern != pattern {
		t.Fatalf("AllowedPattern = %q, want %q", md.AllowedPattern, pattern)
	}
}
