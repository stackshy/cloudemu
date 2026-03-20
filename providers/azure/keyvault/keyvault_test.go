package keyvault

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/secrets/driver"
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func createTestSecret(t *testing.T, m *Mock, name, value string) *driver.SecretInfo {
	t.Helper()

	cfg := driver.SecretConfig{Name: name, Description: "test secret"}

	info, err := m.CreateSecret(context.Background(), cfg, []byte(value))
	requireNoError(t, err)

	return info
}

func TestCreateSecret(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.SecretConfig
		value     []byte
		expectErr bool
	}{
		{
			name:      "valid secret",
			cfg:       driver.SecretConfig{Name: "my-secret", Description: "desc"},
			value:     []byte("secret-value"),
			expectErr: false,
		},
		{
			name:      "empty name",
			cfg:       driver.SecretConfig{Name: ""},
			value:     []byte("val"),
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			info, err := m.CreateSecret(context.Background(), tc.cfg, tc.value)
			assertError(t, err, tc.expectErr)

			if !tc.expectErr {
				assertNotEmpty(t, info.ID)
				assertEqual(t, tc.cfg.Name, info.Name)
				assertEqual(t, tc.cfg.Description, info.Description)
				assertNotEmpty(t, info.ARN)
				assertNotEmpty(t, info.CreatedAt)
				assertNotEmpty(t, info.UpdatedAt)
			}
		})
	}
}

func TestCreateSecretDuplicate(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "dup-secret", "val1")

	cfg := driver.SecretConfig{Name: "dup-secret"}
	_, err := m.CreateSecret(context.Background(), cfg, []byte("val2"))

	assertError(t, err, true)
}

func TestCreateSecretWithTags(t *testing.T) {
	m := newTestMock()
	tags := map[string]string{"env": "prod", "team": "platform"}

	cfg := driver.SecretConfig{
		Name: "tagged-secret",
		Tags: tags,
	}

	info, err := m.CreateSecret(context.Background(), cfg, []byte("val"))
	requireNoError(t, err)

	assertEqual(t, "prod", info.Tags["env"])
	assertEqual(t, "platform", info.Tags["team"])
}

func TestCreateSecretARNFormat(t *testing.T) {
	m := newTestMock()

	info := createTestSecret(t, m, "arn-secret", "val")

	if len(info.ARN) == 0 {
		t.Fatal("expected non-empty ARN (vault URL)")
	}
}

func TestDeleteSecret(t *testing.T) {
	tests := []struct {
		name      string
		setup     bool
		secretNm  string
		expectErr bool
	}{
		{
			name:      "existing secret",
			setup:     true,
			secretNm:  "del-secret",
			expectErr: false,
		},
		{
			name:      "not found",
			setup:     false,
			secretNm:  "nonexistent",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			if tc.setup {
				createTestSecret(t, m, tc.secretNm, "val")
			}

			err := m.DeleteSecret(context.Background(), tc.secretNm)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestGetSecret(t *testing.T) {
	tests := []struct {
		name      string
		setup     bool
		secretNm  string
		expectErr bool
	}{
		{
			name:      "existing secret",
			setup:     true,
			secretNm:  "get-secret",
			expectErr: false,
		},
		{
			name:      "not found",
			setup:     false,
			secretNm:  "missing",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			if tc.setup {
				createTestSecret(t, m, tc.secretNm, "val")
			}

			info, err := m.GetSecret(context.Background(), tc.secretNm)
			assertError(t, err, tc.expectErr)

			if !tc.expectErr {
				assertEqual(t, tc.secretNm, info.Name)
				assertNotEmpty(t, info.ID)
			}
		})
	}
}

func TestListSecrets(t *testing.T) {
	m := newTestMock()

	list, err := m.ListSecrets(context.Background())
	requireNoError(t, err)
	assertEqual(t, 0, len(list))

	createTestSecret(t, m, "s1", "v1")
	createTestSecret(t, m, "s2", "v2")
	createTestSecret(t, m, "s3", "v3")

	list, err = m.ListSecrets(context.Background())
	requireNoError(t, err)
	assertEqual(t, 3, len(list))
}

func TestPutSecretValue(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "put-secret", "initial")

	ver, err := m.PutSecretValue(context.Background(), "put-secret", []byte("updated"))
	requireNoError(t, err)

	assertNotEmpty(t, ver.VersionID)
	assertEqual(t, true, ver.Current)
	assertEqual(t, "updated", string(ver.Value))
}

func TestPutSecretValueNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.PutSecretValue(context.Background(), "no-such-secret", []byte("val"))

	assertError(t, err, true)
}

func TestPutSecretValueMarksOldNotCurrent(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "ver-secret", "v1")

	_, err := m.PutSecretValue(context.Background(), "ver-secret", []byte("v2"))
	requireNoError(t, err)

	versions, err := m.ListSecretVersions(context.Background(), "ver-secret")
	requireNoError(t, err)
	assertEqual(t, 2, len(versions))

	currentCount := 0

	for _, v := range versions {
		if v.Current {
			currentCount++
		}
	}

	assertEqual(t, 1, currentCount)
}

func TestGetSecretValueEmptyVersionID(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "gv-secret", "my-value")

	ver, err := m.GetSecretValue(context.Background(), "gv-secret", "")
	requireNoError(t, err)

	assertEqual(t, true, ver.Current)
	assertEqual(t, "my-value", string(ver.Value))
}

func TestGetSecretValueSpecificVersion(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "sv-secret", "v1")

	v2, err := m.PutSecretValue(context.Background(), "sv-secret", []byte("v2"))
	requireNoError(t, err)

	got, err := m.GetSecretValue(context.Background(), "sv-secret", v2.VersionID)
	requireNoError(t, err)

	assertEqual(t, v2.VersionID, got.VersionID)
	assertEqual(t, "v2", string(got.Value))
}

func TestGetSecretValueNonexistentVersion(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "nv-secret", "val")

	_, err := m.GetSecretValue(context.Background(), "nv-secret", "bad-version-id")

	assertError(t, err, true)
}

func TestGetSecretValueNonexistentSecret(t *testing.T) {
	m := newTestMock()

	_, err := m.GetSecretValue(context.Background(), "no-secret", "")

	assertError(t, err, true)
}

func TestListSecretVersions(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "lv-secret", "v1")

	_, err := m.PutSecretValue(context.Background(), "lv-secret", []byte("v2"))
	requireNoError(t, err)

	_, err = m.PutSecretValue(context.Background(), "lv-secret", []byte("v3"))
	requireNoError(t, err)

	versions, err := m.ListSecretVersions(context.Background(), "lv-secret")
	requireNoError(t, err)
	assertEqual(t, 3, len(versions))
}

func TestListSecretVersionsNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.ListSecretVersions(context.Background(), "missing")

	assertError(t, err, true)
}

func TestListSecretVersionsPrivacy(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "priv-secret", "sensitive")

	versions, err := m.ListSecretVersions(context.Background(), "priv-secret")
	requireNoError(t, err)
	assertEqual(t, 1, len(versions))

	for _, v := range versions {
		if len(v.Value) != 0 {
			t.Error("expected empty Value in listed versions for privacy")
		}
	}
}

func TestMultipleVersionsOnlyLatestCurrent(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "multi-secret", "v1")

	for i := 0; i < 5; i++ {
		_, err := m.PutSecretValue(context.Background(), "multi-secret", []byte("update"))
		requireNoError(t, err)
	}

	versions, err := m.ListSecretVersions(context.Background(), "multi-secret")
	requireNoError(t, err)
	assertEqual(t, 6, len(versions))

	currentCount := 0

	for _, v := range versions {
		if v.Current {
			currentCount++
		}
	}

	assertEqual(t, 1, currentCount)
}

func TestDeleteThenGetReturnsError(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "dtg-secret", "val")

	err := m.DeleteSecret(context.Background(), "dtg-secret")
	requireNoError(t, err)

	_, err = m.GetSecret(context.Background(), "dtg-secret")
	assertError(t, err, true)
}

func TestCreateSecretTagsCopied(t *testing.T) {
	m := newTestMock()
	tags := map[string]string{"key": "original"}

	cfg := driver.SecretConfig{Name: "tag-copy", Tags: tags}

	info, err := m.CreateSecret(context.Background(), cfg, []byte("val"))
	requireNoError(t, err)

	tags["key"] = "mutated"

	assertEqual(t, "original", info.Tags["key"])
}
