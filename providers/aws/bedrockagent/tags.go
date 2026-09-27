package bedrockagent

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// Tag constraints from the service model (TagKey, TagValue, TagKeyList).
const (
	maxTagKeyLen   = 128
	maxTagValueLen = 256
	maxUntagKeys   = 200
)

// tagCharPattern is the TagKey and TagValue character pattern from the
// service model, quoted in the validation message.
const tagCharPattern = `[a-zA-Z0-9\s._:/=+@-]*`

// tagChars enforces tagCharPattern over the whole key or value.
var tagChars = regexp.MustCompile(`^` + tagCharPattern + `$`)

// arnFields is the number of colon-separated fields in an ARN; the last one
// is the resource ("agent/ID", "prompt/ID:1", ...).
const arnFields = 6

// aliasARNIDs is the id count in an agent-alias ARN: agent-alias/AGENT/ALIAS.
const aliasARNIDs = 2

// taggableARNPattern is the TaggableResourcesArn pattern the service model
// publishes, quoted verbatim in the validation message.
const taggableARNPattern = `.*(^arn:aws:bedrock:[a-zA-Z0-9-]+:/d{12}:(agent|agent-alias|knowledge-base|flow|prompt)/[A-Z0-9]{10}` +
	`(?:/[A-Z0-9]{10})?$|^arn:aws:bedrock:[a-zA-Z0-9-]+:/d{12}:flow/([A-Z0-9]{10})/alias/([A-Z0-9]{10})$|` +
	`^arn:aws:bedrock:[a-zA-Z0-9-]+:/d{12}:prompt/([A-Z0-9]{10})?(?::/d+)?$).*`

// taggableARN matches the resource ARNs bedrock-agent tags: agents, agent
// aliases, knowledge bases, flows, flow aliases and prompts (optionally a
// prompt version). The model's pattern writes \d as /d; this is the intent.
var taggableARN = regexp.MustCompile(`^arn:aws:bedrock:[a-zA-Z0-9-]+:\d{12}:(` +
	`agent/[A-Z0-9]{10}|agent-alias/[A-Z0-9]{10}/[A-Z0-9]{10}|knowledge-base/[A-Z0-9]{10}|` +
	`flow/[A-Z0-9]{10}(/alias/[A-Z0-9]{10})?|prompt/[A-Z0-9]{10}(:\d+)?)$`)

// TagResource merges tags onto a bedrock-agent resource named by its ARN.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	var v violations

	v.required("tags", tags == nil)

	if err := v.err(); err != nil {
		return err
	}

	if err := checkTaggableShape(resourceARN); err != nil {
		return err
	}

	if err := validateTags(tags); err != nil {
		return err
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	// Resolve the resource under tagMu. A delete removes the resource first
	// and then takes tagMu to drop its tags, so it either lands before this
	// check (not found) or waits and drops what is written here.
	if err := m.checkExists(resourceARN); err != nil {
		return err
	}

	merged, _ := m.tags.Get(resourceARN)
	merged = maps.Clone(merged)

	if merged == nil {
		merged = make(map[string]string, len(tags))
	}

	maps.Copy(merged, tags)
	m.tags.Set(resourceARN, merged)

	return nil
}

// UntagResource removes tag keys from a resource. Keys it does not carry are
// ignored, as in AWS.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	var v violations

	v.required("tagKeys", tagKeys == nil)

	if err := v.err(); err != nil {
		return err
	}

	if err := checkTaggableShape(resourceARN); err != nil {
		return err
	}

	if err := validateTagKeys(tagKeys); err != nil {
		return err
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	if err := m.checkExists(resourceARN); err != nil {
		return err
	}

	current, ok := m.tags.Get(resourceARN)
	if !ok {
		return nil
	}

	remaining := maps.Clone(current)
	for _, k := range tagKeys {
		delete(remaining, k)
	}

	if len(remaining) == 0 {
		m.tags.Delete(resourceARN)

		return nil
	}

	m.tags.Set(resourceARN, remaining)

	return nil
}

// ListTagsForResource returns a copy of the resource's tags (empty, not nil,
// when it has none).
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	if err := checkTaggableShape(resourceARN); err != nil {
		return nil, err
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	if err := m.checkExists(resourceARN); err != nil {
		return nil, err
	}

	current, _ := m.tags.Get(resourceARN)

	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}

	return out, nil
}

// checkTaggableShape validates the ARN against the taggable-resource pattern.
func checkTaggableShape(resourceARN string) error {
	if taggableARN.MatchString(resourceARN) {
		return nil
	}

	var v violations

	v.addf("Value '%s' at 'resourceArn' failed to satisfy constraint: "+
		"Member must satisfy regular expression pattern: %s", resourceARN, taggableARNPattern)

	return v.err()
}

// checkExists resolves a well-formed ARN to a live resource. Callers hold
// tagMu so the answer stays true until their tag write lands.
func (m *Mock) checkExists(resourceARN string) error {
	if !m.arnExists(resourceARN) {
		return errors.Newf(errors.NotFound, "resource %q not found", resourceARN)
	}

	return nil
}

// arnExists reports whether a taggable ARN names a stored resource. Flow
// aliases and prompt versions are not modeled yet, so their ARNs never resolve.
func (m *Mock) arnExists(resourceARN string) bool {
	parts := strings.SplitN(resourceARN, ":", arnFields)
	if len(parts) != arnFields {
		return false
	}

	kind, rest, _ := strings.Cut(parts[arnFields-1], "/")
	ids := strings.Split(rest, "/")

	var stored string

	switch kind {
	case "agent":
		stored = storedARN(m.agents, ids, 1, func(a *driver.Agent) string { return a.ARN })
	case "agent-alias":
		stored = storedARN(m.aliases, ids, aliasARNIDs, func(a *driver.AgentAlias) string { return a.ARN })
	case "knowledge-base":
		stored = storedARN(m.knowledge, ids, 1, func(kb *driver.KnowledgeBase) string { return kb.ARN })
	case "flow":
		stored = storedARN(m.flows, ids, 1, func(f *driver.Flow) string { return f.ARN })
	case "prompt":
		stored = storedARN(m.prompts, ids, 1, func(p *driver.Prompt) string { return p.ARN })
	}

	return stored != "" && stored == resourceARN
}

// storedARN returns the ARN of the resource whose id is the last of want ids,
// or "" when the id count is wrong or nothing is stored under it.
func storedARN[V any](store *memstore.Store[V], ids []string, want int, arnOf func(V) string) string {
	if len(ids) != want {
		return ""
	}

	v, ok := store.Get(ids[want-1])
	if !ok {
		return ""
	}

	return arnOf(v)
}

// validateTags applies the TagKey and TagValue length and character
// constraints.
func validateTags(tags map[string]string) error {
	var v violations

	for _, k := range slices.Sorted(maps.Keys(tags)) {
		val := tags[k]

		if k == "" || len(k) > maxTagKeyLen {
			v.addf("Value '%s' at 'tags' failed to satisfy constraint: Map keys must satisfy constraint: "+
				"[Member must have length less than or equal to %d, Member must have length greater than or equal to 1]",
				k, maxTagKeyLen)
		}

		if !tagChars.MatchString(k) {
			v.addf("Value '%s' at 'tags' failed to satisfy constraint: Map keys must satisfy constraint: "+
				"[Member must satisfy regular expression pattern: %s]", k, tagCharPattern)
		}

		if len(val) > maxTagValueLen {
			v.addf("Value '%s' at 'tags.%s' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", val, k, maxTagValueLen)
		}

		if !tagChars.MatchString(val) {
			v.addf("Value '%s' at 'tags.%s' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: %s", val, k, tagCharPattern)
		}
	}

	return v.err()
}

// validateTagKeys applies the TagKeyList size bound and the TagKey
// constraints to UntagResource's keys.
func validateTagKeys(keys []string) error {
	var v violations

	if len(keys) > maxUntagKeys {
		v.addf("Value '[%s]' at 'tagKeys' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", strings.Join(keys, ", "), maxUntagKeys)
	}

	for _, k := range keys {
		if k == "" || len(k) > maxTagKeyLen || !tagChars.MatchString(k) {
			v.addf("Value '%s' at 'tagKeys' failed to satisfy constraint: Member must satisfy constraint: "+
				"[Member must have length less than or equal to %d, Member must have length greater than or equal to 1, "+
				"Member must satisfy regular expression pattern: %s]", k, maxTagKeyLen, tagCharPattern)
		}
	}

	return v.err()
}

// putTags stores the tags a Create call carried. Callers validate them first.
// A delete that ran between the store write and here leaves no resource, so
// nothing is written.
func (m *Mock) putTags(resourceARN string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	if !m.arnExists(resourceARN) {
		return
	}

	m.tags.Set(resourceARN, maps.Clone(tags))
}

// dropTags forgets a deleted resource's tags. Callers remove the resource
// from its store first, so a tag write racing the delete either sees it gone
// or is dropped here. Store locks are held only inside each memstore call,
// never while waiting on tagMu, so the two cannot invert.
func (m *Mock) dropTags(resourceARN string) {
	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	m.tags.Delete(resourceARN)
}
