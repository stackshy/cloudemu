package lambda

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestAliasAndVersionNotFoundParity: UpdateAlias on a missing alias uses real
// Lambda's "Alias not found: <function ARN>:<alias>" text; DeleteAlias of a
// missing alias and DeleteFunction of a missing numbered version succeed
// without changing anything, while a missing function is still NotFound.
func TestAliasAndVersionNotFoundParity(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateAlias(ctx, driver.AliasConfig{FunctionName: "my-func", Name: "live", FunctionVersion: "1"}); err != nil {
		t.Fatal(err)
	}

	_, err := m.UpdateAlias(ctx, driver.AliasConfig{FunctionName: "my-func", Name: "nope", FunctionVersion: "1"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("UpdateAlias(missing) err = %v, want NotFound", err)
	}

	if got, want := cerrors.Message(err),
		"Alias not found: arn:aws:lambda:us-east-1:123456789012:function:my-func:nope"; got != want {
		t.Errorf("UpdateAlias message = %q, want %q", got, want)
	}

	if err := m.DeleteAlias(ctx, "my-func", "nope"); err != nil {
		t.Errorf("DeleteAlias(missing alias) err = %v, want nil", err)
	}

	if _, err := m.GetAlias(ctx, "my-func", "live"); err != nil {
		t.Errorf("DeleteAlias(missing alias) touched another alias: %v", err)
	}

	if err := m.DeleteAlias(ctx, "ghost", "live"); !cerrors.IsNotFound(err) {
		t.Errorf("DeleteAlias(missing function) err = %v, want NotFound", err)
	}

	if err := m.DeleteVersion(ctx, "my-func", "99"); err != nil {
		t.Errorf("DeleteVersion(missing version) err = %v, want nil", err)
	}

	if err := m.DeleteVersion(ctx, "ghost", "1"); !cerrors.IsNotFound(err) {
		t.Errorf("DeleteVersion(missing function) err = %v, want NotFound", err)
	}

	err = m.DeleteVersion(ctx, "my-func", "not-an-alias")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("DeleteVersion(unknown non-numeric qualifier) err = %v, want NotFound", err)
	}

	// Worded like the other not-found errors: the qualified function ARN.
	if got, want := cerrors.Message(err),
		"Function not found: arn:aws:lambda:us-east-1:123456789012:function:my-func:not-an-alias"; got != want {
		t.Errorf("DeleteVersion(unknown non-numeric qualifier) message = %q, want %q", got, want)
	}
}
