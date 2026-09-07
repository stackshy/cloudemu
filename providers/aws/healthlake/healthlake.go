// Package healthlake provides an in-memory mock of the AWS HealthLake control
// plane: FHIR data stores and their resource tags. A data store is created
// immediately with a stable id, arn and endpoint and is ACTIVE at once; storing
// FHIR resources and running import/export jobs are out of scope. The computed
// fields clients and IaC read back — the data-store id, arn, endpoint, status
// and createdAt — are minted once at create and stored, so repeated reads never
// drift. The SSE, preload and identity-provider configuration blocks round-trip
// verbatim.
package healthlake

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// Compile-time check that Mock implements driver.HealthLake.
var _ driver.HealthLake = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 20

// datastoreIDBytes is the number of random bytes rendered into a data-store id;
// HealthLake ids are 32 lowercase hex characters.
const datastoreIDBytes = 16

// Mock is an in-memory implementation of the AWS HealthLake control plane.
type Mock struct {
	datastores *memstore.Store[driver.Datastore]
	opts       *config.Options
}

// New creates a new HealthLake mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		datastores: memstore.New[driver.Datastore](),
		opts:       opts,
	}
}

// datastoreARN mints the stable ARN for a data store:
// arn:aws:healthlake:<region>:<acct>:datastore/fhir/<id>.
func (m *Mock) datastoreARN(id string) string {
	return idgen.AWSARN("healthlake", m.opts.Region, m.opts.AccountID, "datastore/fhir/"+id)
}

// datastoreEndpoint mints the stable endpoint for a data store:
// https://healthlake.<region>.amazonaws.com/datastore/<id>/r4/.
func (m *Mock) datastoreEndpoint(id string) string {
	return "https://healthlake." + m.opts.Region + ".amazonaws.com/datastore/" + id + "/r4/"
}

// newDatastoreID mints a random 32-character lowercase hex data-store id.
func newDatastoreID() string {
	b := make([]byte, datastoreIDBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "00000000000000000000000000000000"
	}

	return hex.EncodeToString(b)
}

func copyTags(in []driver.Tag) []driver.Tag {
	if in == nil {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

func copySSE(in *driver.SseConfiguration) *driver.SseConfiguration {
	if in == nil {
		return nil
	}

	out := driver.SseConfiguration{}

	if in.KmsEncryptionConfig != nil {
		kms := *in.KmsEncryptionConfig
		out.KmsEncryptionConfig = &kms
	}

	return &out
}

func copyPreload(in *driver.PreloadDataConfig) *driver.PreloadDataConfig {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyIdentityProvider(in *driver.IdentityProviderConfiguration) *driver.IdentityProviderConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// copyDatastore returns an alias-free copy of a data store, so callers cannot
// mutate stored state through the result.
func copyDatastore(d *driver.Datastore) driver.Datastore {
	out := *d
	out.Tags = copyTags(d.Tags)
	out.SseConfiguration = copySSE(d.SseConfiguration)
	out.PreloadDataConfig = copyPreload(d.PreloadDataConfig)
	out.IdentityProviderConfiguration = copyIdentityProvider(d.IdentityProviderConfiguration)

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
