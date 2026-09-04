package gcs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	hmacStateActive   = "ACTIVE"
	hmacStateInactive = "INACTIVE"

	// hmacAccessIDBytes / hmacSecretBytes size the random material behind a
	// key's access id and secret. Real GCS access ids are ~61 chars ("GOOG1E" +
	// base32) and secrets are 40-char base64; these approximate that shape.
	hmacAccessIDBytes = 24
	hmacSecretBytes   = 30
	hmacAccessPrefix  = "GOOG1E"
)

// hmacKeyStore mirrors the server-side capability the GCS wire handler
// type-asserts for; declaring it here compile-checks the Mock keeps the exact
// signatures the handler needs.
var _ interface {
	CreateHMACKeyGCS(ctx context.Context, projectID, serviceAccountEmail string) (metadata []byte, secret string, err error)
	ListHMACKeysGCS(ctx context.Context, projectID, serviceAccountEmail string, showDeleted bool) ([]byte, error)
	GetHMACKeyGCS(ctx context.Context, projectID, accessID string) ([]byte, error)
	UpdateHMACKeyStateGCS(ctx context.Context, projectID, accessID, state string) ([]byte, error)
	DeleteHMACKeyGCS(ctx context.Context, projectID, accessID string) error
} = (*Mock)(nil)

// hmacKeyRecord is one stored service-account HMAC key. The secret is retained
// only so a snapshot/restore preserves it; it is returned over the wire once, at
// create time.
type hmacKeyRecord struct {
	AccessID            string `json:"accessId"`
	Secret              string `json:"secret"`
	ProjectID           string `json:"projectId"`
	ServiceAccountEmail string `json:"serviceAccountEmail"`
	State               string `json:"state"`
	TimeCreated         string `json:"timeCreated"`
	Updated             string `json:"updated"`
	Etag                string `json:"etag"`
}

// hmacMetaJSON is the metadata contract exchanged with the wire handler (which
// unmarshals a matching struct and renders the storage#hmacKeyMetadata
// resource). The secret is never part of it.
type hmacMetaJSON struct {
	AccessID            string `json:"accessId"`
	ProjectID           string `json:"projectId"`
	ServiceAccountEmail string `json:"serviceAccountEmail"`
	State               string `json:"state"`
	TimeCreated         string `json:"timeCreated"`
	Updated             string `json:"updated"`
	Etag                string `json:"etag"`
}

func (r *hmacKeyRecord) meta() hmacMetaJSON {
	return hmacMetaJSON{
		AccessID:            r.AccessID,
		ProjectID:           r.ProjectID,
		ServiceAccountEmail: r.ServiceAccountEmail,
		State:               r.State,
		TimeCreated:         r.TimeCreated,
		Updated:             r.Updated,
		Etag:                r.Etag,
	}
}

// CreateHMACKeyGCS mints a new ACTIVE HMAC key for a service account and returns
// its metadata JSON plus the secret (surfaced exactly once, at create).
func (m *Mock) CreateHMACKeyGCS(
	_ context.Context, projectID, serviceAccountEmail string,
) (metadata []byte, secret string, err error) {
	if serviceAccountEmail == "" {
		return nil, "", cerrors.New(cerrors.InvalidArgument, "serviceAccountEmail is required")
	}

	now := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)
	rec := &hmacKeyRecord{
		AccessID:            hmacAccessPrefix + randomToken(hmacAccessIDBytes),
		Secret:              randomToken(hmacSecretBytes),
		ProjectID:           projectID,
		ServiceAccountEmail: serviceAccountEmail,
		State:               hmacStateActive,
		TimeCreated:         now,
		Updated:             now,
	}
	rec.Etag = rec.AccessID

	m.hmacKeys.Set(rec.AccessID, rec)

	meta, err := json.Marshal(rec.meta())
	if err != nil {
		return nil, "", err
	}

	return meta, rec.Secret, nil
}

// ListHMACKeysGCS returns the metadata (JSON array) of the project's HMAC keys,
// optionally filtered to a single service account. Deleted keys are pruned on
// delete, so showDeleted has no additional entries to surface here.
func (m *Mock) ListHMACKeysGCS(_ context.Context, projectID, serviceAccountEmail string, _ bool) ([]byte, error) {
	metas := make([]hmacMetaJSON, 0)

	for _, rec := range m.hmacKeys.All() {
		if rec.ProjectID != projectID {
			continue
		}

		if serviceAccountEmail != "" && rec.ServiceAccountEmail != serviceAccountEmail {
			continue
		}

		metas = append(metas, rec.meta())
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].AccessID < metas[j].AccessID })

	return json.Marshal(metas)
}

// GetHMACKeyGCS returns one key's metadata JSON, or NotFound.
func (m *Mock) GetHMACKeyGCS(_ context.Context, projectID, accessID string) ([]byte, error) {
	rec, ok := m.hmacKeys.Get(accessID)
	if !ok || rec.ProjectID != projectID {
		return nil, cerrors.Newf(cerrors.NotFound, "hmac key %q not found", accessID)
	}

	return json.Marshal(rec.meta())
}

// UpdateHMACKeyStateGCS transitions a key between ACTIVE and INACTIVE and returns
// its updated metadata JSON.
func (m *Mock) UpdateHMACKeyStateGCS(_ context.Context, projectID, accessID, state string) ([]byte, error) {
	if state != hmacStateActive && state != hmacStateInactive {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid HMAC key state %q", state)
	}

	var (
		out    []byte
		opErr  error
		outErr error
	)

	found := m.hmacKeys.Update(accessID, func(rec *hmacKeyRecord) *hmacKeyRecord {
		if rec.ProjectID != projectID {
			opErr = cerrors.Newf(cerrors.NotFound, "hmac key %q not found", accessID)
			return rec
		}

		next := *rec
		next.State = state
		next.Updated = m.opts.Clock.Now().UTC().Format(gcsTimeFormat)
		out, outErr = json.Marshal(next.meta())

		return &next
	})

	switch {
	case !found:
		return nil, cerrors.Newf(cerrors.NotFound, "hmac key %q not found", accessID)
	case opErr != nil:
		return nil, opErr
	case outErr != nil:
		return nil, outErr
	}

	return out, nil
}

// DeleteHMACKeyGCS removes a key. Real GCS requires the key be INACTIVE first;
// deleting an ACTIVE key is rejected.
func (m *Mock) DeleteHMACKeyGCS(_ context.Context, projectID, accessID string) error {
	var opErr error

	found := m.hmacKeys.UpdateOrDelete(accessID, func(rec *hmacKeyRecord) (*hmacKeyRecord, bool) {
		if rec.ProjectID != projectID {
			opErr = cerrors.Newf(cerrors.NotFound, "hmac key %q not found", accessID)
			return rec, true
		}

		if rec.State != hmacStateInactive {
			opErr = cerrors.New(cerrors.InvalidArgument, "cannot delete an ACTIVE HMAC key; set it to INACTIVE first")
			return rec, true
		}

		return rec, false
	})

	if !found {
		return cerrors.Newf(cerrors.NotFound, "hmac key %q not found", accessID)
	}

	return opErr
}

// randomToken returns an uppercase, URL-safe token derived from n random bytes,
// suitable for the synthetic access ids and secrets these keys carry.
func randomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)

	return base64.RawURLEncoding.EncodeToString(buf)
}
