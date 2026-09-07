// Package aoss provides an in-memory mock of the Amazon OpenSearch Serverless
// (aoss) control plane: collections plus the encryption/network security
// policies and data access policies that govern them, and resource tags. A
// collection is created immediately in the ACTIVE state with a stable id
// ([a-z0-9]{20}), arn, collectionEndpoint, dashboardEndpoint and createdDate;
// running a search engine is out of scope. Creating a collection requires a
// matching encryption security policy, mirroring real OpenSearch Serverless.
package aoss

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// Compile-time check that Mock implements driver.AOSS.
var _ driver.AOSS = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none. OpenSearch
// Serverless documents a default of 20 for its list operations.
const defaultMaxResults = 20

// defaultStandbyReplicas is the value reported for a collection that did not
// request one. ENABLED is the real-cloud default and, being computed once and
// stored, never drifts an IaC plan.
const defaultStandbyReplicas = "ENABLED"

// awsOwnedKeyMarker is the kmsKeyArn a collection reports when its matching
// encryption policy uses an AWS-owned key (the real API returns "auto").
const awsOwnedKeyMarker = "auto"

// collectionIDBytes is the number of random bytes rendered as hex into a
// collection id: twenty lowercase hex characters, within the [a-z0-9]{3,40}
// contract the API documents.
const collectionIDBytes = 10

// policyVersionBytes is the number of random bytes base64-encoded into a policy
// version token. Sixteen bytes render to a 24-character token, within the
// documented 20–36 length and base64 pattern.
const policyVersionBytes = 16

// Mock is an in-memory implementation of the OpenSearch Serverless control plane.
type Mock struct {
	collections      *memstore.Store[driver.Collection]
	securityPolicies *memstore.Store[driver.Policy]
	accessPolicies   *memstore.Store[driver.Policy]
	opts             *config.Options
}

// New creates a new OpenSearch Serverless mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		collections:      memstore.New[driver.Collection](),
		securityPolicies: memstore.New[driver.Policy](),
		accessPolicies:   memstore.New[driver.Policy](),
		opts:             opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// newCollectionID mints a fresh collection id of twenty lowercase hex
// characters. Minted once at create and stored, so every field derived from it
// (arn, endpoints) is stable across reads.
func newCollectionID() string {
	b := make([]byte, collectionIDBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "00000000000000000000"
	}

	return fmt.Sprintf("%x", b)
}

// newPolicyVersion mints a fresh opaque policy version token. A new token on
// every create and update lets IaC detect a changed policy while the value
// stays stable across reads.
func newPolicyVersion() string {
	b := make([]byte, policyVersionBytes)
	if _, err := rand.Read(b); err != nil {
		return "AAAAAAAAAAAAAAAAAAAAAA=="
	}

	return base64.StdEncoding.EncodeToString(b)
}

// collectionARN mints the stable ARN for a collection
// (arn:aws:aoss:{region}:{acct}:collection/{id}).
func (m *Mock) collectionARN(id string) string {
	return idgen.AWSARN("aoss", m.opts.Region, m.opts.AccountID, "collection/"+id)
}

// collectionEndpoint mints the stable data-plane endpoint for a collection.
func (m *Mock) collectionEndpoint(id string) string {
	return fmt.Sprintf("https://%s.%s.aoss.amazonaws.com", id, m.opts.Region)
}

// dashboardEndpoint mints the stable OpenSearch Dashboards endpoint.
func (m *Mock) dashboardEndpoint(id string) string {
	return m.collectionEndpoint(id) + "/_dashboards"
}

// policyKey composes the store key for a policy from its type and name; a policy
// name is unique per type.
func policyKey(policyType, name string) string {
	return policyType + "/" + name
}

func copyTags(in []driver.Tag) []driver.Tag {
	if in == nil {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

func copyRaw(in []byte) []byte {
	if in == nil {
		return nil
	}

	return append([]byte(nil), in...)
}

// copyCollection returns an alias-free copy of a collection so callers cannot
// mutate stored state through the result.
func copyCollection(c *driver.Collection) driver.Collection {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

// copyPolicy returns an alias-free copy of a policy.
func copyPolicy(p *driver.Policy) driver.Policy {
	out := *p
	out.Policy = copyRaw(p.Policy)

	return out
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
