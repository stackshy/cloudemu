package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestAccessKeyByID(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, driver.UserConfig{Name: "dave"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "dave"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	got, ok := m.AccessKeyByID(ctx, ak.AccessKeyID)
	if !ok {
		t.Fatal("AccessKeyByID: want ok=true for a registered key")
	}

	if got.SecretAccessKey != ak.SecretAccessKey {
		t.Fatalf("secret mismatch: %q != %q", got.SecretAccessKey, ak.SecretAccessKey)
	}
	if got.UserName != "dave" {
		t.Fatalf("UserName: want dave, got %q", got.UserName)
	}
	if got.AccountID != "123456789012" {
		t.Fatalf("AccountID: want 123456789012, got %q", got.AccountID)
	}
	if got.UserARN == "" {
		t.Fatal("UserARN: want the owning user's ARN, got empty")
	}
}

func TestAccessKeyByIDUnknown(t *testing.T) {
	m := newTestMock()
	if _, ok := m.AccessKeyByID(context.Background(), "AKIADOESNOTEXIST0000"); ok {
		t.Fatal("AccessKeyByID: want ok=false for an unknown key")
	}
}
