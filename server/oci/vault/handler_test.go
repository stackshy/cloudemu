package vault_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
	ocivault "github.com/stackshy/cloudemu/v2/server/oci/vault"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	compartment      = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
)

// Compile-time check that the OCI Vault mock carries the OCI-only capabilities
// the handler discovers by type assertion.
var _ ocivault.Extras = (*vaultprovider.Mock)(nil)

type fixture struct {
	t       *testing.T
	handler *ocivault.Handler
	mock    *vaultprovider.Mock
	work    *workrequest.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	opts := config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartment),
	)
	mock := vaultprovider.New(opts)
	work := workrequest.New(opts)

	return &fixture{t: t, handler: ocivault.New(mock, work), mock: mock, work: work}
}

func (f *fixture) do(method, target string, body any) *httptest.ResponseRecorder {
	f.t.Helper()

	var reader *bytes.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	r := httptest.NewRequest(method, target, reader)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	out := map[string]any{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

// newVault creates a vault over the wire and returns its OCID.
func (f *fixture) newVault() string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20180608/vaults", map[string]any{
		"compartmentId": compartment,
		"displayName":   "test-vault",
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	return decode(f.t, w)["id"].(string)
}

// newKey creates a key in a vault over the wire and returns its OCID.
func (f *fixture) newKey(vaultID string) string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20180608/keys?vaultId="+vaultID, map[string]any{
		"compartmentId": compartment,
		"displayName":   "test-key",
		"keyShape":      map[string]any{"algorithm": "AES", "length": 32},
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	return decode(f.t, w)["id"].(string)
}

// newSecret creates a secret over the wire and returns its OCID.
func (f *fixture) newSecret(vaultID, keyID, name, value string) string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20180608/secrets", map[string]any{
		"compartmentId": compartment,
		"vaultId":       vaultID,
		"keyId":         keyID,
		"secretName":    name,
		"secretContent": map[string]any{
			"contentType": "BASE64",
			"content":     base64.StdEncoding.EncodeToString([]byte(value)),
		},
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	return decode(f.t, w)["id"].(string)
}

func TestMatches(t *testing.T) {
	h := ocivault.New(vaultprovider.New(config.NewOptions()), nil)

	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		// The management prefix.
		{name: "vault collection", path: "/20180608/vaults", expect: true},
		{name: "one vault", path: "/20180608/vaults/ocid1.vault.oc1.iad.a", expect: true},
		{name: "vault action", path: "/20180608/vaults/ocid1.vault.oc1.iad.a/actions/scheduleDeletion", expect: true},
		{name: "key collection", path: "/20180608/keys", expect: true},
		{name: "key versions", path: "/20180608/keys/ocid1.key.oc1.iad.a/keyVersions", expect: true},
		{name: "secret collection", path: "/20180608/secrets", expect: true},
		{name: "secret by name", path: "/20180608/secrets/actions/getByName", expect: true},
		{name: "secret versions", path: "/20180608/secrets/ocid1.vaultsecret.oc1.iad.a/versions", expect: true},
		{
			name:   "secret version action",
			path:   "/20180608/secrets/ocid1.vaultsecret.oc1.iad.a/versions/2/actions/scheduleDeletion",
			expect: true,
		},
		{name: "crypto endpoint is claimed to disclose it", path: "/20180608/encrypt", expect: true},
		{name: "sign is claimed to disclose it", path: "/20180608/sign", expect: true},

		// The retrieval prefix.
		{name: "secret bundle", path: "/20190301/secretbundles/ocid1.vaultsecret.oc1.iad.a", expect: true},
		{name: "bundle versions", path: "/20190301/secretbundles/ocid1.vaultsecret.oc1.iad.a/versions", expect: true},
		{name: "bundle by name", path: "/20190301/secretbundles/actions/getByName", expect: true},

		// Not ours.
		{name: "work requests under our prefix", path: "/20180608/workRequests", expect: false},
		{name: "work request poll", path: "/20180608/workRequests/ocid1.workrequest.oc1.iad.a", expect: false},
		{name: "core networking", path: "/20160918/vcns", expect: false},
		{name: "identity", path: "/20160918/users", expect: false},
		{name: "another service's version prefix", path: "/20190301/secrets", expect: false},
		{name: "secrets management on the retrieval prefix", path: "/20190301/vaults", expect: false},
		{name: "bundles on the management prefix", path: "/20180608/secretbundles", expect: false},
		{name: "version only", path: "/20180608", expect: false},
		{name: "root", path: "/", expect: false},
		{name: "unknown collection", path: "/20180608/vaultUsage", expect: false},
		{
			name:   "too many segments",
			path:   "/20180608/secrets/a/versions/2/actions/scheduleDeletion/extra",
			expect: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, h.Matches(httptest.NewRequest(http.MethodGet, tc.path, nil)))
		})
	}
}

// A driver that implements only the portable interface gets a clean 501 rather
// than a panic or a half-served response.
func TestDriverWithoutExtrasIs501(t *testing.T) {
	h := ocivault.New(portableOnly{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/20180608/vaults?compartmentId="+compartment, nil))

	assert.Equal(t, http.StatusNotImplemented, w.Code)
	assert.Contains(t, w.Body.String(), "does not implement OCI Vault")
}

func TestVaultLifecycleOverTheWire(t *testing.T) {
	f := newFixture(t)

	created := f.do(http.MethodPost, "/20180608/vaults", map[string]any{
		"compartmentId": compartment,
		"displayName":   "wire-vault",
		"freeformTags":  map[string]string{"env": "test"},
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	body := decode(t, created)
	id := body["id"].(string)

	assert.Equal(t, "wire-vault", body["displayName"])
	assert.Equal(t, "DEFAULT", body["vaultType"])
	assert.Equal(t, "ACTIVE", body["lifecycleState"])
	assert.NotEmpty(t, created.Header().Get(ocirest.HeaderWorkRequestID))
	assert.NotEmpty(t, created.Header().Get(ocirest.HeaderRequestID))

	got := f.do(http.MethodGet, "/20180608/vaults/"+id, nil)
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, id, decode(t, got)["id"])

	renamed := f.do(http.MethodPut, "/20180608/vaults/"+id, map[string]any{"displayName": "renamed"})
	require.Equal(t, http.StatusOK, renamed.Code)
	assert.Equal(t, "renamed", decode(t, renamed)["displayName"])

	scheduled := f.do(http.MethodPost, "/20180608/vaults/"+id+"/actions/scheduleDeletion", nil)
	require.Equal(t, http.StatusOK, scheduled.Code, scheduled.Body.String())
	assert.Equal(t, "PENDING_DELETION", decode(t, scheduled)["lifecycleState"])
	assert.NotEmpty(t, scheduled.Header().Get(ocirest.HeaderWorkRequestID))

	canceled := f.do(http.MethodPost, "/20180608/vaults/"+id+"/actions/cancelDeletion", nil)
	require.Equal(t, http.StatusOK, canceled.Code)
	assert.Equal(t, "ACTIVE", decode(t, canceled)["lifecycleState"])
}

func TestVaultErrors(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name   string
		method string
		target string
		body   any
		expect int
	}{
		{
			name: "list without a compartment", method: http.MethodGet,
			target: "/20180608/vaults", expect: http.StatusBadRequest,
		},
		{
			name: "create without a compartment", method: http.MethodPost,
			target: "/20180608/vaults", body: map[string]any{"displayName": "v"},
			expect: http.StatusBadRequest,
		},
		{
			name: "create with defined tags", method: http.MethodPost,
			target: "/20180608/vaults",
			body: map[string]any{
				"compartmentId": compartment, "displayName": "v",
				"definedTags": map[string]any{"ns": map[string]any{"k": "v"}},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "create restoring from a backup", method: http.MethodPost,
			target: "/20180608/vaults",
			body: map[string]any{
				"compartmentId": compartment, "displayName": "v",
				"restoreFromFile": map[string]any{"contentLength": 1},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "unknown vault", method: http.MethodGet,
			target: "/20180608/vaults/ocid1.vault.oc1.iad.missing", expect: http.StatusNotFound,
		},
		{
			name: "unknown action", method: http.MethodPost,
			target: "/20180608/vaults/ocid1.vault.oc1.iad.a/actions/restore", expect: http.StatusNotFound,
		},
		{
			name: "delete is not an OCI vault operation", method: http.MethodDelete,
			target: "/20180608/vaults/ocid1.vault.oc1.iad.a", expect: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.method, tc.target, tc.body)
			assert.Equal(t, tc.expect, w.Code, w.Body.String())
			assert.NotEmpty(t, decode(t, w)["code"])
		})
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	f := newFixture(t)

	r := httptest.NewRequest(http.MethodPost, "/20180608/vaults", bytes.NewReader([]byte("{")))
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
}

func TestListVaultsIsScopedToTheCompartment(t *testing.T) {
	f := newFixture(t)
	f.newVault()

	mine := f.do(http.MethodGet, "/20180608/vaults?compartmentId="+compartment, nil)
	require.Equal(t, http.StatusOK, mine.Code)
	assert.Len(t, decodeList(t, mine), 1)

	theirs := f.do(http.MethodGet, "/20180608/vaults?compartmentId="+otherCompartment, nil)
	require.Equal(t, http.StatusOK, theirs.Code)
	assert.Empty(t, decodeList(t, theirs))
}

func TestKeyLifecycleAndRotationOverTheWire(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)

	got := f.do(http.MethodGet, "/20180608/keys/"+keyID, nil)
	require.Equal(t, http.StatusOK, got.Code)

	body := decode(t, got)
	assert.Equal(t, vaultID, body["vaultId"])
	assert.Equal(t, "HSM", body["protectionMode"])

	first := body["currentKeyVersion"].(string)

	rotated := f.do(http.MethodPost, "/20180608/keys/"+keyID+"/keyVersions", nil)
	require.Equal(t, http.StatusOK, rotated.Code, rotated.Body.String())
	assert.NotEqual(t, first, decode(t, rotated)["id"])
	assert.NotEmpty(t, rotated.Header().Get(ocirest.HeaderWorkRequestID))

	versions := f.do(http.MethodGet, "/20180608/keys/"+keyID+"/keyVersions", nil)
	require.Equal(t, http.StatusOK, versions.Code)
	assert.Len(t, decodeList(t, versions), 2)

	one := f.do(http.MethodGet, "/20180608/keys/"+keyID+"/keyVersions/"+first, nil)
	require.Equal(t, http.StatusOK, one.Code)
	assert.Equal(t, first, decode(t, one)["id"])

	scheduled := f.do(http.MethodPost, "/20180608/keys/"+keyID+"/actions/scheduleDeletion",
		map[string]any{"timeOfDeletion": "2026-01-15T00:00:00Z"})
	require.Equal(t, http.StatusOK, scheduled.Code, scheduled.Body.String())
	assert.Equal(t, "PENDING_DELETION", decode(t, scheduled)["lifecycleState"])

	canceled := f.do(http.MethodPost, "/20180608/keys/"+keyID+"/actions/cancelDeletion", nil)
	require.Equal(t, http.StatusOK, canceled.Code)
	assert.Equal(t, "ACTIVE", decode(t, canceled)["lifecycleState"])
}

func TestKeyErrors(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)

	tests := []struct {
		name   string
		method string
		target string
		body   any
		expect int
	}{
		{
			name: "list without a compartment", method: http.MethodGet,
			target: "/20180608/keys", expect: http.StatusBadRequest,
		},
		{
			name: "create without a shape", method: http.MethodPost,
			target: "/20180608/keys?vaultId=" + vaultID,
			body:   map[string]any{"compartmentId": compartment, "displayName": "k"},
			expect: http.StatusBadRequest,
		},
		{
			name: "create with an unknown algorithm", method: http.MethodPost,
			target: "/20180608/keys?vaultId=" + vaultID,
			body: map[string]any{
				"compartmentId": compartment, "displayName": "k",
				"keyShape": map[string]any{"algorithm": "TWOFISH", "length": 32},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "auto rotation is not emulated", method: http.MethodPost,
			target: "/20180608/keys?vaultId=" + vaultID,
			body: map[string]any{
				"compartmentId": compartment, "displayName": "k",
				"keyShape":               map[string]any{"algorithm": "AES", "length": 32},
				"autoKeyRotationDetails": map[string]any{"rotationIntervalInDays": 30},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "reshaping a key is refused", method: http.MethodPut,
			target: "/20180608/keys/" + keyID,
			body:   map[string]any{"keyShape": map[string]any{"algorithm": "AES", "length": 16}},
			expect: http.StatusBadRequest,
		},
		{
			name: "unknown key", method: http.MethodGet,
			target: "/20180608/keys/ocid1.key.oc1.iad.missing", expect: http.StatusNotFound,
		},
		{
			name: "unknown key version", method: http.MethodGet,
			target: "/20180608/keys/" + keyID + "/keyVersions/ocid1.keyversion.oc1.iad.x",
			expect: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.method, tc.target, tc.body)
			assert.Equal(t, tc.expect, w.Code, w.Body.String())
		})
	}
}

func TestSecretLifecycleOverTheWire(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "db-password", "hunter2")

	got := f.do(http.MethodGet, "/20180608/secrets/"+secretID, nil)
	require.Equal(t, http.StatusOK, got.Code)

	body := decode(t, got)
	assert.Equal(t, "db-password", body["secretName"])
	assert.InDelta(t, float64(1), body["currentVersionNumber"], 0.001)

	byName := f.do(http.MethodGet, "/20180608/secrets/actions/getByName?vaultId="+vaultID+"&secretName=db-password", nil)
	require.Equal(t, http.StatusOK, byName.Code, byName.Body.String())
	assert.Equal(t, secretID, decode(t, byName)["id"])

	listed := f.do(http.MethodGet, "/20180608/secrets?compartmentId="+compartment, nil)
	require.Equal(t, http.StatusOK, listed.Code)
	assert.Len(t, decodeList(t, listed), 1)

	elsewhere := f.do(http.MethodGet, "/20180608/secrets?compartmentId="+otherCompartment, nil)
	require.Equal(t, http.StatusOK, elsewhere.Code)
	assert.Empty(t, decodeList(t, elsewhere))

	// A new version through UpdateSecret, then the stages it produced.
	updated := f.do(http.MethodPut, "/20180608/secrets/"+secretID, map[string]any{
		"secretContent": map[string]any{
			"contentType": "BASE64",
			"content":     base64.StdEncoding.EncodeToString([]byte("hunter3")),
			"name":        "v2",
		},
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.InDelta(t, float64(2), decode(t, updated)["currentVersionNumber"], 0.001)

	versions := f.do(http.MethodGet, "/20180608/secrets/"+secretID+"/versions", nil)
	require.Equal(t, http.StatusOK, versions.Code)

	list := decodeList(t, versions)
	require.Len(t, list, 2)
	assert.Equal(t, []any{"PREVIOUS"}, list[0]["stages"])
	assert.Equal(t, []any{"CURRENT", "LATEST"}, list[1]["stages"])

	one := f.do(http.MethodGet, "/20180608/secrets/"+secretID+"/versions/1", nil)
	require.Equal(t, http.StatusOK, one.Code)
	assert.InDelta(t, float64(1), decode(t, one)["versionNumber"], 0.001)
}

// Scheduled deletion and its cancellation, both answered with headers only.
func TestSecretScheduledDeletionOverTheWire(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "doomed", "v")

	scheduled := f.do(http.MethodPost, "/20180608/secrets/"+secretID+"/actions/scheduleDeletion", nil)
	require.Equal(t, http.StatusNoContent, scheduled.Code, scheduled.Body.String())
	assert.NotEmpty(t, scheduled.Header().Get(ocirest.HeaderWorkRequestID))

	got := f.do(http.MethodGet, "/20180608/secrets/"+secretID, nil)
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, "PENDING_DELETION", decode(t, got)["lifecycleState"])
	assert.Equal(t, "2026-01-31T00:00:00Z", decode(t, got)["timeOfDeletion"])

	again := f.do(http.MethodPost, "/20180608/secrets/"+secretID+"/actions/scheduleDeletion", nil)
	assert.Equal(t, http.StatusConflict, again.Code)

	canceled := f.do(http.MethodPost, "/20180608/secrets/"+secretID+"/actions/cancelDeletion", nil)
	require.Equal(t, http.StatusNoContent, canceled.Code)

	back := f.do(http.MethodGet, "/20180608/secrets/"+secretID, nil)
	assert.Equal(t, "ACTIVE", decode(t, back)["lifecycleState"])
}

func TestSecretVersionScheduledDeletionOverTheWire(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "versioned", "one")

	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/20180608/secrets/"+secretID, map[string]any{
		"secretContent": map[string]any{
			"contentType": "BASE64",
			"content":     base64.StdEncoding.EncodeToString([]byte("two")),
		},
	}).Code)

	base := "/20180608/secrets/" + secretID + "/versions/"

	current := f.do(http.MethodPost, base+"2/actions/scheduleDeletion", nil)
	assert.Equal(t, http.StatusConflict, current.Code, current.Body.String())

	scheduled := f.do(http.MethodPost, base+"1/actions/scheduleDeletion", nil)
	require.Equal(t, http.StatusNoContent, scheduled.Code, scheduled.Body.String())
	assert.NotEmpty(t, scheduled.Header().Get(ocirest.HeaderWorkRequestID))

	canceled := f.do(http.MethodPost, base+"1/actions/cancelDeletion", nil)
	require.Equal(t, http.StatusNoContent, canceled.Code)

	bad := f.do(http.MethodPost, base+"abc/actions/scheduleDeletion", nil)
	assert.Equal(t, http.StatusBadRequest, bad.Code)

	unknown := f.do(http.MethodPost, base+"1/actions/restore", nil)
	assert.Equal(t, http.StatusNotFound, unknown.Code)
}

func TestSecretErrors(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)

	content := map[string]any{"contentType": "BASE64", "content": base64.StdEncoding.EncodeToString([]byte("v"))}

	tests := []struct {
		name   string
		method string
		target string
		body   any
		expect int
	}{
		{
			name: "list without a compartment", method: http.MethodGet,
			target: "/20180608/secrets", expect: http.StatusBadRequest,
		},
		{
			name: "create without content", method: http.MethodPost, target: "/20180608/secrets",
			body: map[string]any{
				"compartmentId": compartment, "vaultId": vaultID, "keyId": keyID, "secretName": "s",
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "create with an unsupported content type", method: http.MethodPost, target: "/20180608/secrets",
			body: map[string]any{
				"compartmentId": compartment, "vaultId": vaultID, "keyId": keyID, "secretName": "s",
				"secretContent": map[string]any{"contentType": "PLAINTEXT", "content": "abc"},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "create with content that is not base64", method: http.MethodPost, target: "/20180608/secrets",
			body: map[string]any{
				"compartmentId": compartment, "vaultId": vaultID, "keyId": keyID, "secretName": "s",
				"secretContent": map[string]any{"contentType": "BASE64", "content": "not base64!"},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "secret rules are not emulated", method: http.MethodPost, target: "/20180608/secrets",
			body: map[string]any{
				"compartmentId": compartment, "vaultId": vaultID, "keyId": keyID, "secretName": "s",
				"secretContent": content,
				"secretRules":   []any{map[string]any{"ruleType": "SECRET_EXPIRY_RULE"}},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "rotation config is not emulated", method: http.MethodPost, target: "/20180608/secrets",
			body: map[string]any{
				"compartmentId": compartment, "vaultId": vaultID, "keyId": keyID, "secretName": "s",
				"secretContent": content, "rotationConfig": map[string]any{"targetSystemDetails": map[string]any{}},
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "unknown secret", method: http.MethodGet,
			target: "/20180608/secrets/ocid1.vaultsecret.oc1.iad.missing", expect: http.StatusNotFound,
		},
		{
			name: "getByName without a vault", method: http.MethodGet,
			target: "/20180608/secrets/actions/getByName?secretName=s", expect: http.StatusBadRequest,
		},
		{
			name: "unknown collection action", method: http.MethodGet,
			target: "/20180608/secrets/actions/purge", expect: http.StatusNotFound,
		},
		{
			name: "delete is not an OCI secret operation", method: http.MethodDelete,
			target: "/20180608/secrets/ocid1.vaultsecret.oc1.iad.a", expect: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.method, tc.target, tc.body)
			assert.Equal(t, tc.expect, w.Code, w.Body.String())
		})
	}
}

func TestSecretBundleDataPlane(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "readable", "one")

	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/20180608/secrets/"+secretID, map[string]any{
		"secretContent": map[string]any{
			"contentType": "BASE64",
			"content":     base64.StdEncoding.EncodeToString([]byte("two")),
			"name":        "second",
		},
	}).Code)

	tests := []struct {
		name   string
		target string
		expect string
	}{
		{name: "current by default", target: "/20190301/secretbundles/" + secretID, expect: "two"},
		{name: "by number", target: "/20190301/secretbundles/" + secretID + "?versionNumber=1", expect: "one"},
		{name: "by name", target: "/20190301/secretbundles/" + secretID + "?secretVersionName=second", expect: "two"},
		{name: "by stage", target: "/20190301/secretbundles/" + secretID + "?stage=PREVIOUS", expect: "one"},
		{
			name:   "by vault and secret name",
			target: "/20190301/secretbundles/actions/getByName?vaultId=" + vaultID + "&secretName=readable",
			expect: "two",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(http.MethodGet, tc.target, nil)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			body := decode(t, w)
			content := body["secretBundleContent"].(map[string]any)
			assert.Equal(t, "BASE64", content["contentType"])

			raw, err := base64.StdEncoding.DecodeString(content["content"].(string))
			require.NoError(t, err)
			assert.Equal(t, tc.expect, string(raw))
		})
	}

	versions := f.do(http.MethodGet, "/20190301/secretbundles/"+secretID+"/versions", nil)
	require.Equal(t, http.StatusOK, versions.Code)
	assert.Len(t, decodeList(t, versions), 2)
}

func TestSecretBundleErrors(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "readable", "one")

	tests := []struct {
		name   string
		method string
		target string
		expect int
	}{
		{
			name: "unknown secret", method: http.MethodGet,
			target: "/20190301/secretbundles/ocid1.vaultsecret.oc1.iad.missing", expect: http.StatusNotFound,
		},
		{
			name: "two selectors at once", method: http.MethodGet,
			target: "/20190301/secretbundles/" + secretID + "?versionNumber=1&stage=CURRENT",
			expect: http.StatusBadRequest,
		},
		{
			name: "version number that is not a number", method: http.MethodGet,
			target: "/20190301/secretbundles/" + secretID + "?versionNumber=latest", expect: http.StatusBadRequest,
		},
		{
			name: "no version in that stage", method: http.MethodGet,
			target: "/20190301/secretbundles/" + secretID + "?stage=PENDING", expect: http.StatusNotFound,
		},
		{
			name: "writing through the data plane", method: http.MethodPost,
			target: "/20190301/secretbundles/" + secretID, expect: http.StatusMethodNotAllowed,
		},
		{
			name: "unknown data plane action", method: http.MethodGet,
			target: "/20190301/secretbundles/actions/list", expect: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.method, tc.target, nil)
			assert.Equal(t, tc.expect, w.Code, w.Body.String())
		})
	}
}

func TestChangeCompartmentRecordsAWorkRequest(t *testing.T) {
	f := newFixture(t)
	vaultID := f.newVault()
	keyID := f.newKey(vaultID)
	secretID := f.newSecret(vaultID, keyID, "movable", "v")

	for _, target := range []string{
		"/20180608/vaults/" + vaultID + "/actions/changeCompartment",
		"/20180608/keys/" + keyID + "/actions/changeCompartment",
		"/20180608/secrets/" + secretID + "/actions/changeCompartment",
	} {
		w := f.do(http.MethodPost, target, map[string]any{"compartmentId": otherCompartment})
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		assert.NotEmpty(t, w.Header().Get(ocirest.HeaderWorkRequestID))
	}

	moved := f.do(http.MethodGet, "/20180608/secrets?compartmentId="+otherCompartment, nil)
	require.Equal(t, http.StatusOK, moved.Code)
	assert.Len(t, decodeList(t, moved), 1)

	assert.Len(t, f.work.List(otherCompartment), 3)

	bad := f.do(http.MethodPost, "/20180608/vaults/"+vaultID+"/actions/changeCompartment", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, bad.Code)
}

// The KMS crypto endpoint shares the management prefix; CloudEmu stores no key
// material, so it says so rather than inventing a ciphertext.
func TestCryptoEndpointIsDisclosed(t *testing.T) {
	f := newFixture(t)

	for _, op := range []string{"encrypt", "decrypt", "sign", "verify", "generateDataEncryptionKey", "exportKey"} {
		w := f.do(http.MethodPost, "/20180608/"+op, map[string]any{})
		assert.Equal(t, http.StatusNotImplemented, w.Code, op)
		assert.Contains(t, w.Body.String(), "stores no key material", op)
	}
}

// portableOnly implements the portable driver and nothing else.
type portableOnly struct{}

func (portableOnly) CreateSecret(_ context.Context, _ secretsdriver.SecretConfig, _ []byte) (
	*secretsdriver.SecretInfo, error) {
	return nil, nil //nolint:nilnil // never called; the handler 501s first.
}
func (portableOnly) DeleteSecret(context.Context, string) error { return nil }
func (portableOnly) GetSecret(context.Context, string) (*secretsdriver.SecretInfo, error) {
	return nil, nil //nolint:nilnil // never called; the handler 501s first.
}
func (portableOnly) ListSecrets(context.Context) ([]secretsdriver.SecretInfo, error) { return nil, nil }
func (portableOnly) PutSecretValue(context.Context, string, []byte) (*secretsdriver.SecretVersion, error) {
	return nil, nil //nolint:nilnil // never called; the handler 501s first.
}
func (portableOnly) GetSecretValue(context.Context, string, string) (*secretsdriver.SecretVersion, error) {
	return nil, nil //nolint:nilnil // never called; the handler 501s first.
}
func (portableOnly) ListSecretVersions(context.Context, string) ([]secretsdriver.SecretVersion, error) {
	return nil, nil
}
