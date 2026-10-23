package bedrockagent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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

	if isNull(cfg.StorageConfiguration) {
		return nil
	}

	return validateStorageConfiguration(cfg.StorageConfiguration)
}

// storageTypeMembers maps each storageConfiguration type to the member that
// must carry its settings.
func storageTypeMembers() map[string]string {
	return map[string]string{
		"OPENSEARCH_SERVERLESS":      "opensearchServerlessConfiguration",
		"OPENSEARCH_MANAGED_CLUSTER": "opensearchManagedClusterConfiguration",
		"PINECONE":                   "pineconeConfiguration",
		"REDIS_ENTERPRISE_CLOUD":     "redisEnterpriseCloudConfiguration",
		"RDS":                        "rdsConfiguration",
		"MONGO_DB_ATLAS":             "mongoDbAtlasConfiguration",
		"NEPTUNE_ANALYTICS":          "neptuneAnalyticsConfiguration",
		"S3_VECTORS":                 "s3VectorsConfiguration",
	}
}

// validateStorageConfiguration checks that the storage type comes with its
// matching configuration member.
func validateStorageConfiguration(raw json.RawMessage) error {
	var sc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sc); err != nil {
		return errors.New(errors.InvalidArgument, "storageConfiguration is not a valid object")
	}

	var typ string
	if t, ok := sc["type"]; ok && !isNull(t) {
		if err := json.Unmarshal(t, &typ); err != nil {
			return errors.New(errors.InvalidArgument, "storageConfiguration.type is not a string")
		}
	}

	var v violations

	v.required("storageConfiguration.type", typ == "")

	if member, ok := storageTypeMembers()[typ]; ok {
		v.required("storageConfiguration."+member, isNull(sc[member]))
	}

	return v.err()
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
func newID[V any](store *memstore.Store[V]) (string, error) {
	for {
		id, err := idgen.BedrockAgentResourceID()
		if err != nil {
			return "", err
		}

		if !store.Has(id) {
			return id, nil
		}
	}
}

// Service-model constraints shared by the agent sub-resource, version and
// alias operations.
const (
	namePattern           = `([0-9a-zA-Z][_-]?){1,100}`
	draftPattern          = `DRAFT`
	versionPattern        = `(DRAFT|[0-9]{0,4}[1-9][0-9]{0,4})`
	numericVersionPattern = `[0-9]{1,5}`
	idPattern             = `[0-9a-zA-Z]{10}`
	idempotencyPattern    = `[a-zA-Z0-9](-*[a-zA-Z0-9]){0,256}`
	maxDescriptionLen     = 200
	minClientTokenLen     = 33
	maxClientTokenLen     = 256
)

// Compiled forms of the patterns above, anchored over the whole value.
var (
	nameRE           = regexp.MustCompile(`^` + namePattern + `$`)
	versionRE        = regexp.MustCompile(`^(DRAFT|\d{0,4}[1-9]\d{0,4})$`)
	numericVersionRE = regexp.MustCompile(`^\d{1,5}$`)
	idRE             = regexp.MustCompile(`^` + idPattern + `$`)
	idempotencyRE    = regexp.MustCompile(`^` + idempotencyPattern + `$`)
)

// pattern records a pattern failure for a present value.
func (v *violations) pattern(field, value string, re *regexp.Regexp, text string) {
	if value != "" && !re.MatchString(value) {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy regular expression pattern: %s", value, field, text)
	}
}

// maxLen records a length failure for a value longer than limit.
func (v *violations) maxLen(field, value string, limit int) {
	if len(value) > limit {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", value, field, limit)
	}
}

// enum records an enum failure for a present value outside allowed.
func (v *violations) enum(field, value string, allowed ...string) {
	if value != "" && !slices.Contains(allowed, value) {
		v.addf("Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [%s]", value, field, strings.Join(allowed, ", "))
	}
}

// name checks a required Name-shaped member.
func (v *violations) name(field, value string) {
	v.required(field, value == "")
	v.pattern(field, value, nameRE, namePattern)
}

// description checks an optional description member (1..200 characters).
func (v *violations) description(value string) {
	v.maxLen("description", value, maxDescriptionLen)
}

// clientToken checks an optional idempotency token.
func (v *violations) clientToken(value string) {
	if value == "" {
		return
	}

	if len(value) < minClientTokenLen {
		v.addf("Value '%s' at 'clientToken' failed to satisfy constraint: "+
			"Member must have length greater than or equal to %d", value, minClientTokenLen)
	}

	v.maxLen("clientToken", value, maxClientTokenLen)
	v.pattern("clientToken", value, idempotencyRE, idempotencyPattern)
}

// draft checks that a write targets the DRAFT version.
func (v *violations) draft(value string) {
	v.required("agentVersion", value == "")

	if value != "" && value != driver.DraftVersion {
		v.addf("Value '%s' at 'agentVersion' failed to satisfy constraint: "+
			"Member must satisfy regular expression pattern: %s", value, draftPattern)
	}
}

// version checks a read's DRAFT-or-number version member.
func (v *violations) version(field, value string) {
	v.required(field, value == "")
	v.pattern(field, value, versionRE, versionPattern)
}

// numericVersion checks a member that must be a version number.
func (v *violations) numericVersion(field, value string) {
	v.required(field, value == "")
	v.pattern(field, value, numericVersionRE, numericVersionPattern)
}

// id checks a required 10-character resource id member.
func (v *violations) id(field, value string) {
	v.required(field, value == "")
	v.pattern(field, value, idRE, idPattern)
}

// uniqueID mints a 10-character id for which taken reports false.
func uniqueID(taken func(id string) bool) (string, error) {
	for {
		id, err := idgen.BedrockAgentResourceID()
		if err != nil {
			return "", err
		}

		if !taken(id) {
			return id, nil
		}
	}
}

// nextVersion allocates the next version number of the resource keyed by key
// (versions start at 1 and are never reused). Callers hold m.mu.
func (m *Mock) nextVersion(key string) string {
	n, _ := m.versionSeq.Get(key)
	n++
	m.versionSeq.Set(key, n)

	return strconv.Itoa(n)
}

// versionKey joins the parts of a composite store key.
func versionKey(parts ...string) string {
	return strings.Join(parts, "/")
}

// compareVersions orders DRAFT first, then numbered versions ascending.
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}

	if a == driver.DraftVersion {
		return -1
	}

	if b == driver.DraftVersion {
		return 1
	}

	ai, _ := strconv.Atoi(a)
	bi, _ := strconv.Atoi(b)

	return cmp.Compare(ai, bi)
}

// regexpAgentAliasID is the AgentAliasId pattern.
var regexpAgentAliasID = regexp.MustCompile(`^` + `(\bTSTALIASID\b|[0-9a-zA-Z]+)` + `$`)
