// Package kms provides an in-memory mock implementation of AWS KMS.
package kms

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// Compile-time check that Mock implements driver.KMS.
var _ driver.KMS = (*Mock)(nil)

const (
	defaultPendingWindowDays = 30
	minPendingWindowDays     = 7
	maxPendingWindowDays     = 30
	hoursPerDay              = 24
)

// keyData is the full server-side state of a key: its public metadata plus the
// secret material and settings never exposed in KeyMetadata.
type keyData struct {
	meta   driver.KeyMetadata
	tags   map[string]string
	policy string

	// material holds the raw key bytes for symmetric/HMAC keys; asymmetric
	// keys keep their parsed key in signer (populated in the crypto phase).
	material []byte

	mu sync.RWMutex
}

// aliasData is the stored form of an alias.
type aliasData struct {
	name        string
	arn         string
	targetKeyID string
	created     time.Time
	updated     time.Time
}

// Mock is an in-memory implementation of AWS KMS.
type Mock struct {
	keys    *memstore.Store[*keyData]
	aliases *memstore.Store[*aliasData]
	opts    *config.Options
}

// New creates a new KMS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		keys:    memstore.New[*keyData](),
		aliases: memstore.New[*aliasData](),
		opts:    opts,
	}
}

// newKeyID returns a UUID-shaped key identifier. It is derived from the shared
// id counter, so it is unique and deterministic under a fake clock/reset.
func newKeyID() string {
	n := idgen.GenerateID("")
	return fmt.Sprintf("%s-0000-4000-8000-%012s", n, n)
}

func (m *Mock) keyARN(keyID string) string {
	return idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, "key/"+keyID)
}

func (m *Mock) aliasARN(name string) string {
	return idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, name)
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// resolveKeyID turns any accepted key reference — key ID, key ARN, alias name
// ("alias/foo"), or alias ARN — into the underlying key ID.
func (m *Mock) resolveKeyID(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "arn:"):
		// arn:aws:kms:region:acct:key/<id> or .../alias/<name>
		idx := strings.Index(ref, ":key/")
		if idx >= 0 {
			return ref[idx+len(":key/"):], nil
		}

		aidx := strings.Index(ref, ":alias/")
		if aidx >= 0 {
			return m.aliasTarget("alias/" + ref[aidx+len(":alias/"):])
		}

		return "", errors.Newf(errors.InvalidArgument, "unrecognized key ARN %q", ref)
	case strings.HasPrefix(ref, "alias/"):
		return m.aliasTarget(ref)
	default:
		return ref, nil
	}
}

func (m *Mock) aliasTarget(name string) (string, error) {
	a, ok := m.aliases.Get(name)
	if !ok {
		return "", errors.Newf(errors.NotFound, "alias %q not found", name)
	}

	return a.targetKeyID, nil
}

// getKey resolves a reference and returns the live key, erroring if it is
// missing.
func (m *Mock) getKey(ref string) (*keyData, error) {
	id, err := m.resolveKeyID(ref)
	if err != nil {
		return nil, err
	}

	kd, ok := m.keys.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "key %q not found", ref)
	}

	return kd, nil
}
