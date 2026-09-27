package bedrockagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Bounds on maxResults shared by every bedrock-agent List operation.
const (
	minPageResults = 1
	maxPageResults = 1000
)

// knowledgeBaseTypeVector is the knowledgeBaseConfiguration type that needs a
// vector store in storageConfiguration.
const knowledgeBaseTypeVector = "VECTOR"

// violations collects constraint failures and renders them the way AWS does:
// "N validation error(s) detected: <v1>; <v2>".
type violations []string

// required records a null-member failure for field when missing is true.
func (v *violations) required(field string, missing bool) {
	if missing {
		*v = append(*v, fmt.Sprintf("Value null at '%s' failed to satisfy constraint: Member must not be null", field))
	}
}

// addf records a formatted constraint failure.
func (v *violations) addf(format string, args ...any) {
	*v = append(*v, fmt.Sprintf(format, args...))
}

// err returns the ValidationException for the collected failures, or nil.
func (v violations) err() error {
	switch len(v) {
	case 0:
		return nil
	case 1:
		return errors.New(errors.InvalidArgument, "1 validation error detected: "+v[0])
	default:
		return errors.Newf(errors.InvalidArgument, "%d validation errors detected: %s", len(v), strings.Join(v, "; "))
	}
}

// isNull reports whether a raw JSON member is absent or an explicit null.
func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// validateKnowledgeBase checks the members CreateKnowledgeBase and
// UpdateKnowledgeBase require, plus the vector store a VECTOR base needs.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateKnowledgeBase(cfg driver.KnowledgeBaseConfig) error {
	var v violations

	v.required("name", cfg.Name == "")
	v.required("roleArn", cfg.RoleArn == "")
	v.required("knowledgeBaseConfiguration", isNull(cfg.KnowledgeBaseConfiguration))

	if err := v.err(); err != nil {
		return err
	}

	var kbc struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(cfg.KnowledgeBaseConfiguration, &kbc); err != nil {
		return errors.New(errors.InvalidArgument, "knowledgeBaseConfiguration is not a valid object")
	}

	if kbc.Type == knowledgeBaseTypeVector && isNull(cfg.StorageConfiguration) {
		return errors.New(errors.InvalidArgument,
			"storageConfiguration is required for a knowledge base of type VECTOR")
	}

	return nil
}

// paginate slices one page of items (already in a stable order) per the
// caller's maxResults and nextToken, enforcing the 1..1000 bound. An absent
// maxResults returns up to the maximum page.
func paginate[T any](items []T, page driver.Page) (pageItems []T, nextToken string, err error) {
	size := maxPageResults

	if page.MaxResults != nil {
		n := *page.MaxResults

		var v violations

		switch {
		case n < minPageResults:
			v.addf("Value '%d' at 'maxResults' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d", n, minPageResults)
		case n > maxPageResults:
			v.addf("Value '%d' at 'maxResults' failed to satisfy constraint: "+
				"Member must have value less than or equal to %d", n, maxPageResults)
		}

		if verr := v.err(); verr != nil {
			return nil, "", verr
		}

		size = int(n)
	}

	p, perr := pagination.Paginate(items, page.NextToken, size)
	if perr != nil {
		return nil, "", errors.New(errors.InvalidArgument, "invalid nextToken")
	}

	if p.Items == nil {
		p.Items = []T{}
	}

	return p.Items, p.NextPageToken, nil
}

// newID mints a 10-character bedrock-agent id not already used in store.
func newID[V any](store *memstore.Store[V]) string {
	for {
		id := idgen.BedrockAgentResourceID()
		if !store.Has(id) {
			return id
		}
	}
}
