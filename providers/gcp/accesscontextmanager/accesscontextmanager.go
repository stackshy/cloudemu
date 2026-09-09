// Package accesscontextmanager provides an in-memory mock of the Google Access
// Context Manager control plane (accesscontextmanager.googleapis.com/v1). It
// models organization-scoped access policies and their nested access levels and
// service perimeters (VPC Service Controls), and the long-running operations
// their mutating RPCs return. It is control-plane only: IAM verbs, the
// replaceAll/commit batch verbs, gcpUserAccessBindings, and authorizedOrgsDescs
// are out of scope.
package accesscontextmanager

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

var _ acmdriver.AccessContextManager = (*Mock)(nil)

const (
	accessLevelsColl      = "accessLevels"
	servicePerimetersColl = "servicePerimeters"

	// policyNumberSpan and policyNumberBase bound a minted policy number to a
	// stable 13-digit range, matching the shape of a real access-policy number.
	policyNumberSpan = 9_000_000_000_000
	policyNumberBase = 1_000_000_000_000

	// etagBytes is the number of hash bytes an opaque etag is derived from.
	etagBytes = 12

	// uint64Bytes is the width of a big-endian uint64 seed used in etag minting.
	uint64Bytes = 8
)

// Mock is the in-memory Access Context Manager control-plane implementation.
// Policies are keyed by their full resource name (accessPolicies/{number});
// children by accessPolicies/{number}/{coll}/{id}.
type Mock struct {
	mu sync.RWMutex

	policies   *memstore.Store[acmdriver.Policy]
	levels     *memstore.Store[acmdriver.Child]
	perimeters *memstore.Store[acmdriver.Child]
	operations *memstore.Store[acmdriver.Operation]

	opSeq   atomic.Uint64
	etagSeq atomic.Uint64
	opts    *config.Options
}

// New creates a new Access Context Manager mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		policies:   memstore.New[acmdriver.Policy](),
		levels:     memstore.New[acmdriver.Child](),
		perimeters: memstore.New[acmdriver.Child](),
		operations: memstore.New[acmdriver.Operation](),
		opts:       opts,
	}
}

// policyName builds the full resource name of a policy.
func policyName(number string) string { return "accessPolicies/" + number }

// childName builds the full resource name of a child (level or perimeter).
func childName(coll, policyNumber, id string) string {
	return "accessPolicies/" + policyNumber + "/" + coll + "/" + id
}

// policyNumber mints the stable numeric name component for a policy,
// deterministically from its parent+title so it is reproducible, then stored so
// a read never recomputes it.
func policyNumber(parent, title string) string {
	sum := sha256.Sum256([]byte(parent + "\x00" + title))
	n := binary.BigEndian.Uint64(sum[:8])%policyNumberSpan + policyNumberBase

	return strconv.FormatUint(n, 10)
}

// nextEtag returns a fresh opaque etag. It is minted at each mutation and stored
// on the resource, so reads are byte-stable and a patch changes it (matching the
// real API's optimistic-concurrency etag).
func (m *Mock) nextEtag(name string) string {
	seq := m.etagSeq.Add(1)

	buf := make([]byte, uint64Bytes)
	binary.BigEndian.PutUint64(buf, seq)
	sum := sha256.Sum256(append([]byte(name+"\x00"), buf...))

	return base64.StdEncoding.EncodeToString(sum[:etagBytes])
}

// newOp records a completed operation and returns it. The caller holds the write
// lock.
func (m *Mock) newOp(kind, opType, target string) *acmdriver.Operation {
	op := acmdriver.Operation{
		Name:       "operations/" + strconv.FormatUint(m.opSeq.Add(1), 10) + "-" + idgen.UUID(),
		Done:       true,
		TargetName: target,
		Kind:       kind,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*acmdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &acmdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// HasOperation reports whether name was minted by this backend.
func (m *Mock) HasOperation(_ context.Context, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.operations.Has(name)
}

// notFound builds the NOT_FOUND error carrying the full resource name.
func notFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "resource %q not found", name)
}
