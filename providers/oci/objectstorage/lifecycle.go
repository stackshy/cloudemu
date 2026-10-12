package objectstorage

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Lifecycle rule actions OCI accepts.
const (
	LifecycleDelete     = "DELETE"
	LifecycleArchive    = "ARCHIVE"
	LifecycleInfrequent = "INFREQUENT_ACCESS"
	LifecycleAbort      = "ABORT"
)

// Lifecycle rule targets. An empty target is OCI's default, objects.
const (
	TargetObjects          = "objects"
	TargetPreviousVersions = "previous-object-versions"
	TargetMultipartUploads = "multipart-uploads"
)

// Lifecycle time units.
const (
	UnitDays  = "DAYS"
	UnitYears = "YEARS"
)

// LifecycleRule is one OCI object lifecycle rule, kept exactly as the caller
// sent it so a policy reads back as it was written.
type LifecycleRule struct {
	Name              string
	Action            string
	TimeAmount        int64
	TimeUnit          string
	Target            string
	IsEnabled         bool
	InclusionPrefixes []string
	InclusionPatterns []string
	ExclusionPatterns []string
}

// LifecyclePolicy is a bucket's object lifecycle policy.
type LifecyclePolicy struct {
	Rules       []LifecycleRule
	TimeCreated string
}

// PutLifecyclePolicy replaces a bucket's lifecycle policy. Every rule is
// validated whole: an action, target, unit or pattern CloudEmu cannot honor
// is refused rather than stored and ignored.
func (m *Mock) PutLifecyclePolicy(_ context.Context, bucket string, rules []LifecycleRule) (*LifecyclePolicy, error) {
	for i := range rules {
		if err := validateLifecycleRule(&rules[i]); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	bkt.lifecycle = &LifecyclePolicy{Rules: cloneLifecycleRules(rules), TimeCreated: m.now()}

	return cloneLifecyclePolicy(bkt.lifecycle), nil
}

// GetLifecyclePolicy returns a bucket's lifecycle policy.
func (m *Mock) GetLifecyclePolicy(_ context.Context, bucket string) (*LifecyclePolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	if bkt.lifecycle == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no lifecycle policy for bucket %q", bucket)
	}

	return cloneLifecyclePolicy(bkt.lifecycle), nil
}

// DeleteLifecyclePolicy removes a bucket's lifecycle policy.
func (m *Mock) DeleteLifecyclePolicy(_ context.Context, bucket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	if bkt.lifecycle == nil {
		return cerrors.Newf(cerrors.NotFound, "no lifecycle policy for bucket %q", bucket)
	}

	bkt.lifecycle = nil

	return nil
}

func validateLifecycleRule(r *LifecycleRule) error {
	if r.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "lifecycle rule name is required")
	}

	if r.TimeAmount <= 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "rule %s: timeAmount must be positive", r.Name)
	}

	if r.TimeUnit != UnitDays && r.TimeUnit != UnitYears {
		return cerrors.Newf(cerrors.InvalidArgument, "rule %s: unsupported timeUnit %q, want DAYS or YEARS", r.Name, r.TimeUnit)
	}

	if err := validateLifecycleAction(r); err != nil {
		return err
	}

	for _, p := range append(append([]string{}, r.InclusionPatterns...), r.ExclusionPatterns...) {
		if _, err := globRegexp(p); err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "rule %s: invalid pattern %q", r.Name, p)
		}
	}

	return nil
}

// validateLifecycleAction checks the target and the action against it.
func validateLifecycleAction(r *LifecycleRule) error {
	switch r.Target {
	case "", TargetObjects, TargetPreviousVersions, TargetMultipartUploads:
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "rule %s: unsupported target %q", r.Name, r.Target)
	}

	switch r.Action {
	case LifecycleDelete, LifecycleArchive, LifecycleInfrequent:
		if r.Target == TargetMultipartUploads {
			return cerrors.Newf(cerrors.InvalidArgument,
				"rule %s: the multipart-uploads target only takes the ABORT action", r.Name)
		}
	case LifecycleAbort:
		if r.Target != TargetMultipartUploads {
			return cerrors.Newf(cerrors.InvalidArgument,
				"rule %s: ABORT requires the multipart-uploads target", r.Name)
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "rule %s: unsupported lifecycle action %q", r.Name, r.Action)
	}

	return nil
}

// lifecycleSpan converts a rule's time amount to a duration. A year is
// 365 days, the same reckoning retention rules use.
func lifecycleSpan(amount int64, unit string) (time.Duration, error) {
	switch unit {
	case UnitDays:
		return time.Duration(amount) * hoursPerDay * time.Hour, nil
	case UnitYears:
		return time.Duration(amount) * daysPerYear * hoursPerDay * time.Hour, nil
	default:
		return 0, cerrors.Newf(cerrors.InvalidArgument, "unsupported timeUnit %q, want DAYS or YEARS", unit)
	}
}

// EvaluateLifecycle reports the current object names an enabled DELETE rule
// targeting live objects has aged out. It reports rather than deletes, as the
// other providers' mocks do; a rule aimed at previous versions or multipart
// uploads never selects a live object.
func (m *Mock) EvaluateLifecycle(_ context.Context, bucket string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	if bkt.lifecycle == nil {
		return nil, nil
	}

	now := m.opts.Clock.Now().UTC()

	var expired []string

	for _, name := range bkt.objects.Keys() {
		obj, ok := bkt.objects.Get(name)
		if !ok {
			continue
		}

		if objectExpired(obj, bkt.lifecycle.Rules, now) {
			expired = append(expired, name)
		}
	}

	sort.Strings(expired)

	return expired, nil
}

func objectExpired(obj *objectData, rules []LifecycleRule, now time.Time) bool {
	modified, err := time.Parse(timeFormat, obj.TimeModified)
	if err != nil {
		return false
	}

	age := now.Sub(modified)

	for i := range rules {
		r := &rules[i]
		if !r.IsEnabled || r.Action != LifecycleDelete || (r.Target != "" && r.Target != TargetObjects) {
			continue
		}

		if !ruleSelects(r, obj.Name) {
			continue
		}

		if span, spanErr := lifecycleSpan(r.TimeAmount, r.TimeUnit); spanErr == nil && age >= span {
			return true
		}
	}

	return false
}

// ruleSelects applies a rule's object-name filter: the name must carry one of
// the inclusion prefixes and match one of the inclusion patterns, when either
// list is given, and must match none of the exclusion patterns.
func ruleSelects(r *LifecycleRule, name string) bool {
	if len(r.InclusionPrefixes) > 0 && !anyPrefix(name, r.InclusionPrefixes) {
		return false
	}

	if len(r.InclusionPatterns) > 0 && !anyPattern(name, r.InclusionPatterns) {
		return false
	}

	return !anyPattern(name, r.ExclusionPatterns)
}

func anyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}

	return false
}

func anyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if re, err := globRegexp(p); err == nil && re.MatchString(name) {
			return true
		}
	}

	return false
}

// globRegexp compiles an OCI object-name pattern: * matches any run of
// characters, slashes included, ? matches one, and [...] a character class.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder

	b.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				return nil, cerrors.New(cerrors.InvalidArgument, "unterminated character class")
			}

			b.WriteString(pattern[i : i+end+1])
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	b.WriteString("$")

	return regexp.Compile(b.String())
}

// PutLifecycleConfig is the portable form: each portable rule becomes the OCI
// rules that express its expiration, transition and multipart-abort settings.
func (m *Mock) PutLifecycleConfig(ctx context.Context, bucket string, cfg driver.LifecycleConfig) error {
	var rules []LifecycleRule

	for _, r := range cfg.Rules {
		converted := fromPortableRule(r)
		if len(converted) == 0 {
			return cerrors.Newf(cerrors.InvalidArgument,
				"lifecycle rule %s sets no expiration, transition or multipart abort", r.ID)
		}

		rules = append(rules, converted...)
	}

	_, err := m.PutLifecyclePolicy(ctx, bucket, rules)

	return err
}

//nolint:gocritic // driver.LifecycleRule is the portable value type, iterated by value.
func fromPortableRule(r driver.LifecycleRule) []LifecycleRule {
	base := LifecycleRule{Name: r.ID, IsEnabled: r.Enabled, TimeUnit: UnitDays}
	if r.Prefix != "" {
		base.InclusionPrefixes = []string{r.Prefix}
	}

	var out []LifecycleRule

	add := func(suffix, action, target string, days int) {
		rule := base
		rule.Name += suffix
		rule.Action, rule.Target, rule.TimeAmount = action, target, int64(days)

		rule.InclusionPrefixes = cloneStrings(base.InclusionPrefixes)
		out = append(out, rule)
	}

	if r.ExpirationDays > 0 {
		add("", LifecycleDelete, TargetObjects, r.ExpirationDays)
	}

	if r.TransitionDays > 0 {
		action := LifecycleArchive
		if r.TransitionStorageClass == LifecycleInfrequent || r.TransitionStorageClass == TierInfrequentAccess {
			action = LifecycleInfrequent
		}

		add("-transition", action, TargetObjects, r.TransitionDays)
	}

	if r.AbortMultipartDays > 0 {
		add("-abort", LifecycleAbort, TargetMultipartUploads, r.AbortMultipartDays)
	}

	return out
}

// GetLifecycleConfig is the portable read. A rule the portable shape cannot
// carry — a non-object target, patterns, several prefixes or a YEARS unit — is
// reported rather than flattened into one that would mean something else.
func (m *Mock) GetLifecycleConfig(ctx context.Context, bucket string) (*driver.LifecycleConfig, error) {
	policy, err := m.GetLifecyclePolicy(ctx, bucket)
	if err != nil {
		return nil, err
	}

	out := &driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, 0, len(policy.Rules))}

	for i := range policy.Rules {
		r := &policy.Rules[i]

		rule, ok := toPortableRule(r)
		if !ok {
			return nil, cerrors.Newf(cerrors.Unimplemented,
				"lifecycle rule %s uses a target, filter or unit the portable lifecycle shape cannot express; "+
					"read it with GetLifecyclePolicy", r.Name)
		}

		out.Rules = append(out.Rules, rule)
	}

	return out, nil
}

// portable reports whether a rule's unit and filter fit the portable shape:
// days, at most one prefix, no patterns.
func portable(r *LifecycleRule) bool {
	return r.TimeUnit == UnitDays && len(r.InclusionPatterns) == 0 && len(r.ExclusionPatterns) == 0 &&
		len(r.InclusionPrefixes) <= 1
}

func toPortableRule(r *LifecycleRule) (driver.LifecycleRule, bool) {
	if !portable(r) {
		return driver.LifecycleRule{}, false
	}

	out := driver.LifecycleRule{ID: r.Name, Enabled: r.IsEnabled}
	if len(r.InclusionPrefixes) == 1 {
		out.Prefix = r.InclusionPrefixes[0]
	}

	days := int(r.TimeAmount)

	switch {
	case r.Action == LifecycleAbort && r.Target == TargetMultipartUploads:
		out.AbortMultipartDays = days
	case r.Target != "" && r.Target != TargetObjects:
		return driver.LifecycleRule{}, false
	case r.Action == LifecycleDelete:
		out.ExpirationDays = days
	default:
		out.TransitionDays, out.TransitionStorageClass = days, r.Action
	}

	return out, true
}

func cloneLifecycleRules(in []LifecycleRule) []LifecycleRule {
	out := make([]LifecycleRule, len(in))

	for i := range in {
		out[i] = in[i]
		out[i].InclusionPrefixes = cloneStrings(in[i].InclusionPrefixes)
		out[i].InclusionPatterns = cloneStrings(in[i].InclusionPatterns)
		out[i].ExclusionPatterns = cloneStrings(in[i].ExclusionPatterns)
	}

	return out
}

func cloneLifecyclePolicy(p *LifecyclePolicy) *LifecyclePolicy {
	return &LifecyclePolicy{Rules: cloneLifecycleRules(p.Rules), TimeCreated: p.TimeCreated}
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}
