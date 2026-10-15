package iam

import (
	"context"
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

var secretAccessKeyShape = regexp.MustCompile(`^[A-Za-z0-9+/]{40}$`)

func TestCreateAccessKeySecretIsRandom(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, driver.UserConfig{Name: "erin"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	first, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	second, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	for _, ak := range []*driver.AccessKeyInfo{first, second} {
		if !secretAccessKeyShape.MatchString(ak.SecretAccessKey) {
			t.Fatalf("secret %q, want 40 base64-alphabet chars", ak.SecretAccessKey)
		}
	}

	if first.SecretAccessKey == second.SecretAccessKey {
		t.Fatal("two access keys share a secret")
	}
}
