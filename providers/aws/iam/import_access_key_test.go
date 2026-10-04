package iam

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestImportAccessKey(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.ImportAccessKey(ctx, "ghost", "AKIAGHOST", "s"); !cerrors.IsNotFound(err) {
		t.Fatalf("import for a missing user = %v, want NotFound", err)
	}

	if _, err := m.CreateUser(ctx, driver.UserConfig{Name: "boot"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := m.ImportAccessKey(ctx, "boot", "", "s"); err == nil {
		t.Fatal("import with an empty id: want an error")
	}

	if err := m.ImportAccessKey(ctx, "boot", "AKIABOOT", "boot-secret"); err != nil {
		t.Fatalf("ImportAccessKey: %v", err)
	}

	got, ok := m.AccessKeyByID(ctx, "AKIABOOT")
	if !ok || got.SecretAccessKey != "boot-secret" || got.UserName != "boot" {
		t.Fatalf("AccessKeyByID = %+v ok=%v", got, ok)
	}

	if err := m.ImportAccessKey(ctx, "boot", "AKIABOOT", "other"); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate import = %v, want AlreadyExists", err)
	}

	if err := m.ImportAccessKey(ctx, "boot", "AKIABOOT2", "s2"); err != nil {
		t.Fatalf("second key: %v", err)
	}

	if err := m.ImportAccessKey(ctx, "boot", "AKIABOOT3", "s3"); err == nil {
		t.Fatal("third key: want the per-user quota error")
	}

	keys, err := m.ListAccessKeys(ctx, "boot")
	if err != nil || len(keys) != 2 || keys[0].Status != "Active" {
		t.Fatalf("ListAccessKeys = %+v, %v", keys, err)
	}
}
