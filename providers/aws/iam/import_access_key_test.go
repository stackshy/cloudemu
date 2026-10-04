package iam

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// TestImportAccessKeyConcurrentSameID checks that concurrent imports of one id
// leave exactly one winner and never overwrite the stored secret.
func TestImportAccessKeyConcurrentSameID(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	const n = 16

	for i := range n {
		if _, err := m.CreateUser(ctx, driver.UserConfig{Name: fmt.Sprintf("u%d", i)}); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}

	var (
		wg   sync.WaitGroup
		wins atomic.Int32
	)

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if m.ImportAccessKey(ctx, fmt.Sprintf("u%d", i), "AKIARACE", fmt.Sprintf("s%d", i)) == nil {
				wins.Add(1)
			}
		}()
	}

	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d concurrent imports succeeded, want 1", got)
	}

	ak, _ := m.AccessKeyByID(ctx, "AKIARACE")
	if "s"+strings.TrimPrefix(ak.UserName, "u") != ak.SecretAccessKey {
		t.Fatalf("stored key pairs owner %s with secret %s from another import", ak.UserName, ak.SecretAccessKey)
	}
}
