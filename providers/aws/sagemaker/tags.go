package sagemaker

import (
	"context"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// Tag limits (AWS General Reference, SageMaker tags).
const (
	maxTagsPerResource = 50
	maxTagKeyLen       = 128
	maxTagValueLen     = 256
)

// setTags records the tags a resource was created with. It always overwrites,
// so a resource re-created under a name whose previous incarnation was deleted
// never inherits that one's tags.
func (m *Mock) setTags(arn string, tags []driver.Tag) {
	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	if len(tags) == 0 {
		m.tags.Delete(arn)

		return
	}

	m.tags.Set(arn, copyTags(tags))
}

// dropTags removes every tag of a deleted resource.
func (m *Mock) dropTags(arn string) {
	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	m.tags.Delete(arn)
}

// validateTags enforces the per-tag length limits.
func validateTags(tags []driver.Tag) error {
	for _, t := range tags {
		if t.Key == "" || len(t.Key) > maxTagKeyLen {
			return errors.Newf(errors.InvalidArgument,
				"Value '%s' at 'tags.key' failed to satisfy constraint: Member must have length between 1 and %d",
				t.Key, maxTagKeyLen)
		}

		if len(t.Value) > maxTagValueLen {
			return errors.Newf(errors.InvalidArgument,
				"Value at 'tags.value' failed to satisfy constraint: Member must have length less than or equal to %d",
				maxTagValueLen)
		}
	}

	return nil
}

// requireTaggable returns NotFound unless resourceARN names a live resource of
// a taggable type, so tagging a missing or malformed ARN is never a silent
// success.
func (m *Mock) requireTaggable(resourceARN string) error {
	if !m.arnExists(resourceARN) {
		return errors.Newf(errors.NotFound, "Could not find resource %q", resourceARN)
	}

	return nil
}

// AddTags merges tags onto a resource, overwriting values for existing keys.
func (m *Mock) AddTags(_ context.Context, resourceARN string, tags []driver.Tag) ([]driver.Tag, error) {
	if err := validateTags(tags); err != nil {
		return nil, err
	}

	if err := m.requireTaggable(resourceARN); err != nil {
		return nil, err
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	existing, _ := m.tags.Get(resourceARN)

	merged := make([]driver.Tag, 0, len(existing)+len(tags))
	index := map[string]int{}

	for _, t := range existing {
		index[t.Key] = len(merged)
		merged = append(merged, t)
	}

	for _, t := range tags {
		if i, ok := index[t.Key]; ok {
			merged[i].Value = t.Value

			continue
		}

		index[t.Key] = len(merged)
		merged = append(merged, t)
	}

	if len(merged) > maxTagsPerResource {
		return nil, errors.Newf(errors.InvalidArgument,
			"Value at 'tags' failed to satisfy constraint: Member must have length less than or equal to %d",
			maxTagsPerResource)
	}

	m.tags.Set(resourceARN, merged)

	return copyTags(merged), nil
}

// ListTags returns the tags for a resource ARN.
func (m *Mock) ListTags(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	if err := m.requireTaggable(resourceARN); err != nil {
		return nil, err
	}

	tags, _ := m.tags.Get(resourceARN)

	return copyTags(tags), nil
}

// DeleteTags removes the given tag keys from a resource ARN.
func (m *Mock) DeleteTags(_ context.Context, resourceARN string, keys []string) error {
	if err := m.requireTaggable(resourceARN); err != nil {
		return err
	}

	m.tagMu.Lock()
	defer m.tagMu.Unlock()

	existing, ok := m.tags.Get(resourceARN)
	if !ok {
		return nil
	}

	remove := make(map[string]bool, len(keys))
	for _, k := range keys {
		remove[k] = true
	}

	kept := make([]driver.Tag, 0, len(existing))

	for _, t := range existing {
		if !remove[t.Key] {
			kept = append(kept, t)
		}
	}

	m.tags.Set(resourceARN, kept)

	return nil
}

// arnExists reports whether arn names a live SageMaker resource of a taggable
// type. The ARN resource segment ("training-job/name") selects the store.
func (m *Mock) arnExists(arn string) bool {
	prefix := "arn:aws:sagemaker:" + m.opts.Region + ":" + m.opts.AccountID + ":"
	if !strings.HasPrefix(arn, prefix) {
		return false
	}

	kind, rest, ok := strings.Cut(strings.TrimPrefix(arn, prefix), "/")
	if !ok || rest == "" {
		return false
	}

	check, known := m.arnChecks()[kind]

	return known && check(arn, rest)
}

// arnChecks maps an ARN resource type to a liveness check (full ARN, the part
// after "<type>/"). A new resource family registers its type here.
func (m *Mock) arnChecks() map[string]func(arn, rest string) bool {
	byName := func(has func(string) bool) func(string, string) bool {
		return func(_, rest string) bool { return has(rest) }
	}
	byARN := func(has func(string) bool) func(string, string) bool {
		return func(arn, _ string) bool { return has(arn) }
	}

	checks := map[string]func(string, string) bool{
		"training-job":                       byName(m.trainingJobs.Has),
		"processing-job":                     byName(m.processingJobs.Has),
		"transform-job":                      byName(m.transformJobs.Has),
		"hyper-parameter-tuning-job":         byName(m.tuningJobs.Has),
		"automl-job":                         byName(m.autoMLJobs.Has),
		"labeling-job":                       byName(m.labelingJobs.Has),
		"compilation-job":                    byName(m.compilationJobs.Has),
		"model":                              byName(m.models.Has),
		"endpoint-config":                    byName(m.endpointConfigs.Has),
		"endpoint":                           byName(m.endpoints.Has),
		"inference-component":                byName(m.inferenceComponents.Has),
		"model-package-group":                byName(m.packageGroups.Has),
		"model-package":                      byARN(m.packages.Has),
		"domain":                             byName(m.domains.Has),
		"user-profile":                       m.scopedCheck(m.userProfiles.Has),
		"space":                              m.scopedCheck(m.spaces.Has),
		"app":                                m.appCheck,
		"notebook-instance":                  byName(m.notebooks.Has),
		"notebook-instance-lifecycle-config": byName(m.notebookLCs.Has),
		"code-repository":                    byName(m.codeRepos.Has),
		"cluster":                            byName(m.clusters.Has),
		"feature-group":                      byName(m.featureGroups.Has),
		"pipeline":                           m.pipelineCheck,
		"experiment":                         byName(m.experiments.Has),
		"experiment-trial":                   byName(m.trials.Has),
	}

	for kind, check := range m.familyArnChecks(byName, byARN) {
		checks[kind] = check
	}

	return checks
}

// scopedCheck handles "<domain>/<name>" resource segments.
func (*Mock) scopedCheck(has func(string) bool) func(string, string) bool {
	return func(_, rest string) bool {
		domain, name, ok := strings.Cut(rest, "/")

		return ok && has(scopedKey(domain, name))
	}
}

// appCheck matches a Studio app ARN against the stored apps.
func (m *Mock) appCheck(arn, _ string) bool {
	for _, a := range m.apps.All() {
		if a.AppARN == arn {
			return true
		}
	}

	return false
}

// pipelineCheck handles both "pipeline/<name>" and
// "pipeline/<name>/execution/<id>".
func (m *Mock) pipelineCheck(arn, rest string) bool {
	if name, _, found := strings.Cut(rest, "/execution/"); found {
		return m.pipelines.Has(name) && m.executions.Has(arn)
	}

	return m.pipelines.Has(rest)
}

// sageMakerARNPattern is the shape SageMaker's models constrain every ARN input
// to, so a malformed ARN is a ValidationException before any lookup.
var sageMakerARNPattern = regexp.MustCompile(`^arn:aws[a-z\-]*:sagemaker:[a-z0-9\-]*:[0-9]{12}:[a-z\-]*/.+$`)

const maxARNLen = 256

// validateARN rejects an ARN that is not shaped like a SageMaker ARN. field is
// the API member name used in the message.
func validateARN(field, arn string) error {
	if len(arn) > maxARNLen || !sageMakerARNPattern.MatchString(arn) {
		return errors.Newf(errors.InvalidArgument,
			"Value '%s' at '%s' failed to satisfy constraint: Member must satisfy regular expression pattern: %s",
			arn, field, sageMakerARNPattern.String())
	}

	return nil
}
