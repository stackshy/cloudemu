package secretsmanager

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func createTestSecret(t *testing.T, m *Mock, name, value string) *driver.SecretInfo {
	t.Helper()

	cfg := driver.SecretConfig{Name: name, Description: "test secret"}

	info, err := m.CreateSecret(context.Background(), cfg, []byte(value))
	require.NoError(t, err)

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
			if tc.expectErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}

			assert.NotEmpty(t, info.ID)
			assert.Equal(t, tc.cfg.Name, info.Name)
			assert.Equal(t, tc.cfg.Description, info.Description)
			assert.NotEmpty(t, info.ResourceID)
			assert.NotEmpty(t, info.CreatedAt)
			assert.NotEmpty(t, info.UpdatedAt)
		})
	}
}

func TestCreateSecretDuplicate(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "dup-secret", "val1")

	cfg := driver.SecretConfig{Name: "dup-secret"}
	_, err := m.CreateSecret(context.Background(), cfg, []byte("val2"))

	require.Error(t, err)
}

func TestCreateSecretWithTags(t *testing.T) {
	m := newTestMock()
	tags := map[string]string{"env": "prod", "team": "platform"}

	cfg := driver.SecretConfig{
		Name: "tagged-secret",
		Tags: tags,
	}

	info, err := m.CreateSecret(context.Background(), cfg, []byte("val"))
	require.NoError(t, err)

	assert.Equal(t, "prod", info.Tags["env"])
	assert.Equal(t, "platform", info.Tags["team"])
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
			if tc.expectErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}
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
			if tc.expectErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.secretNm, info.Name)
			assert.NotEmpty(t, info.ID)
		})
	}
}

func TestListSecrets(t *testing.T) {
	m := newTestMock()

	list, err := m.ListSecrets(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, len(list))

	createTestSecret(t, m, "s1", "v1")
	createTestSecret(t, m, "s2", "v2")
	createTestSecret(t, m, "s3", "v3")

	list, err = m.ListSecrets(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, len(list))
}

func TestPutSecretValue(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "put-secret", "initial")

	ver, err := m.PutSecretValue(context.Background(), "put-secret", []byte("updated"))
	require.NoError(t, err)

	assert.NotEmpty(t, ver.VersionID)
	assert.Equal(t, true, ver.Current)
	assert.Equal(t, "updated", string(ver.Value))
}

func TestPutSecretValueNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.PutSecretValue(context.Background(), "no-such-secret", []byte("val"))

	require.Error(t, err)
}

func TestPutSecretValueMarksOldNotCurrent(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "ver-secret", "v1")

	_, err := m.PutSecretValue(context.Background(), "ver-secret", []byte("v2"))
	require.NoError(t, err)

	versions, err := m.ListSecretVersions(context.Background(), "ver-secret")
	require.NoError(t, err)
	assert.Equal(t, 2, len(versions))

	currentCount := 0

	for _, v := range versions {
		if v.Current {
			currentCount++
		}
	}

	assert.Equal(t, 1, currentCount)
}

func TestGetSecretValueEmptyVersionID(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "gv-secret", "my-value")

	ver, err := m.GetSecretValue(context.Background(), "gv-secret", "")
	require.NoError(t, err)

	assert.Equal(t, true, ver.Current)
	assert.Equal(t, "my-value", string(ver.Value))
}

func TestGetSecretValueSpecificVersion(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "sv-secret", "v1")

	v2, err := m.PutSecretValue(context.Background(), "sv-secret", []byte("v2"))
	require.NoError(t, err)

	got, err := m.GetSecretValue(context.Background(), "sv-secret", v2.VersionID)
	require.NoError(t, err)

	assert.Equal(t, v2.VersionID, got.VersionID)
	assert.Equal(t, "v2", string(got.Value))
}

func TestGetSecretValueNonexistentVersion(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "nv-secret", "val")

	_, err := m.GetSecretValue(context.Background(), "nv-secret", "bad-version-id")

	require.Error(t, err)
}

func TestGetSecretValueNonexistentSecret(t *testing.T) {
	m := newTestMock()

	_, err := m.GetSecretValue(context.Background(), "no-secret", "")

	require.Error(t, err)
}

func TestListSecretVersions(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "lv-secret", "v1")

	_, err := m.PutSecretValue(context.Background(), "lv-secret", []byte("v2"))
	require.NoError(t, err)

	_, err = m.PutSecretValue(context.Background(), "lv-secret", []byte("v3"))
	require.NoError(t, err)

	versions, err := m.ListSecretVersions(context.Background(), "lv-secret")
	require.NoError(t, err)
	assert.Equal(t, 3, len(versions))
}

func TestListSecretVersionsNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.ListSecretVersions(context.Background(), "missing")

	require.Error(t, err)
}

func TestListSecretVersionsPrivacy(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "priv-secret", "sensitive")

	versions, err := m.ListSecretVersions(context.Background(), "priv-secret")
	require.NoError(t, err)
	assert.Equal(t, 1, len(versions))

	for _, v := range versions {
		assert.Empty(t, v.Value, "expected empty Value in listed versions for privacy")
	}
}

func TestMultipleVersionsOnlyLatestCurrent(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "multi-secret", "v1")

	for i := 0; i < 5; i++ {
		_, err := m.PutSecretValue(context.Background(), "multi-secret", []byte("update"))
		require.NoError(t, err)
	}

	versions, err := m.ListSecretVersions(context.Background(), "multi-secret")
	require.NoError(t, err)
	assert.Equal(t, 6, len(versions))

	currentCount := 0

	for _, v := range versions {
		if v.Current {
			currentCount++
		}
	}

	assert.Equal(t, 1, currentCount)
}

func TestDeleteThenGetReturnsError(t *testing.T) {
	m := newTestMock()

	createTestSecret(t, m, "dtg-secret", "val")

	err := m.DeleteSecret(context.Background(), "dtg-secret")
	require.NoError(t, err)

	_, err = m.GetSecret(context.Background(), "dtg-secret")
	require.Error(t, err)
}

func TestCreateSecretTagsCopied(t *testing.T) {
	m := newTestMock()
	tags := map[string]string{"key": "original"}

	cfg := driver.SecretConfig{Name: "tag-copy", Tags: tags}

	info, err := m.CreateSecret(context.Background(), cfg, []byte("val"))
	require.NoError(t, err)

	tags["key"] = "mutated"

	assert.Equal(t, "original", info.Tags["key"])
}
