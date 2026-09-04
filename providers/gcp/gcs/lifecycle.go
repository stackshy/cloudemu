package gcs

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// lifecycleRawStore mirrors the server-side capability the GCS wire handler
// type-asserts for; declaring it here gives a compile-time guarantee the Mock
// keeps the exact signatures the handler needs.
var _ interface {
	SetLifecycleGCS(ctx context.Context, bucket string, doc []byte) error
	GetLifecycleGCS(ctx context.Context, bucket string) ([]byte, bool, error)
} = (*Mock)(nil)

const (
	actionDelete   = "Delete"
	actionSetClass = "SetStorageClass"
)

// gcsLifecycleDoc mirrors the GCS JSON lifecycle object ({"rule":[...]}) so the
// evaluator can interpret the full set of rule conditions the wire layer stored
// verbatim (https://cloud.google.com/storage/docs/lifecycle#conditions).
type gcsLifecycleDoc struct {
	Rule []gcsLifecycleRule `json:"rule"`
}

type gcsLifecycleRule struct {
	Action    gcsLifecycleAction    `json:"action"`
	Condition gcsLifecycleCondition `json:"condition"`
}

type gcsLifecycleAction struct {
	Type         string `json:"type"`
	StorageClass string `json:"storageClass,omitempty"`
}

type gcsLifecycleCondition struct {
	Age                     *int     `json:"age,omitempty"`
	CreatedBefore           string   `json:"createdBefore,omitempty"`
	CustomTimeBefore        string   `json:"customTimeBefore,omitempty"`
	DaysSinceCustomTime     *int     `json:"daysSinceCustomTime,omitempty"`
	DaysSinceNoncurrentTime *int     `json:"daysSinceNoncurrentTime,omitempty"`
	NoncurrentTimeBefore    string   `json:"noncurrentTimeBefore,omitempty"`
	IsLive                  *bool    `json:"isLive,omitempty"`
	MatchesStorageClass     []string `json:"matchesStorageClass,omitempty"`
	NumNewerVersions        *int     `json:"numNewerVersions,omitempty"`
	MatchesPrefix           []string `json:"matchesPrefix,omitempty"`
	MatchesSuffix           []string `json:"matchesSuffix,omitempty"`
}

// SetLifecycleGCS stores the bucket's lifecycle configuration verbatim (doc is
// GCS JSON, {"rule":[...]}), preserving every condition. It also derives the
// portable age-only subset so GetLifecycleConfig stays coherent.
func (m *Mock) SetLifecycleGCS(_ context.Context, bucket string, doc []byte) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	raw := append([]byte(nil), doc...)
	derived := deriveLifecycleConfig(raw)

	bkt.mu.Lock()
	bkt.gcsLifecycleRaw = raw
	bkt.lifecycle = derived
	bkt.mu.Unlock()

	return nil
}

// GetLifecycleGCS returns the verbatim lifecycle JSON for a bucket, reporting
// ok=false when none is set.
func (m *Mock) GetLifecycleGCS(_ context.Context, bucket string) (doc []byte, ok bool, err error) {
	bkt, found := m.buckets.Get(bucket)
	if !found {
		return nil, false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if len(bkt.gcsLifecycleRaw) == 0 {
		return nil, false, nil
	}

	return append([]byte(nil), bkt.gcsLifecycleRaw...), true, nil
}

// deriveLifecycleConfig projects the verbatim GCS lifecycle onto the portable
// driver.LifecycleConfig (age-based Delete/SetStorageClass only) so the typed
// GetLifecycleConfig surfaces something coherent. Returns nil when the doc has
// no rules.
func deriveLifecycleConfig(raw []byte) *driver.LifecycleConfig {
	var doc gcsLifecycleDoc
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Rule) == 0 {
		return nil
	}

	cfg := &driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, 0, len(doc.Rule))}

	for i := range doc.Rule {
		r := &doc.Rule[i]
		rule := driver.LifecycleRule{ID: strconv.Itoa(i), Enabled: true}

		if len(r.Condition.MatchesPrefix) > 0 {
			rule.Prefix = r.Condition.MatchesPrefix[0]
		}

		age := 0
		if r.Condition.Age != nil {
			age = *r.Condition.Age
		}

		switch r.Action.Type {
		case actionDelete:
			rule.ExpirationDays = age
		case actionSetClass:
			rule.TransitionDays = age
			rule.TransitionStorageClass = r.Action.StorageClass
		}

		cfg.Rules = append(cfg.Rules, rule)
	}

	return cfg
}

// PutLifecycleConfig sets the portable (age-only) lifecycle configuration. It
// clears any verbatim GCS config so the portable rules become authoritative.
func (m *Mock) PutLifecycleConfig(_ context.Context, bucket string, cfg driver.LifecycleConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	cfgCopy := driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, len(cfg.Rules))}
	copy(cfgCopy.Rules, cfg.Rules)

	bkt.mu.Lock()
	bkt.lifecycle = &cfgCopy
	bkt.gcsLifecycleRaw = nil
	bkt.mu.Unlock()

	return nil
}

// GetLifecycleConfig returns the portable lifecycle configuration.
func (m *Mock) GetLifecycleConfig(_ context.Context, bucket string) (*driver.LifecycleConfig, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	cfg := bkt.lifecycle
	bkt.mu.Unlock()

	if cfg == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no lifecycle configuration for bucket %q", bucket)
	}

	return cfg, nil
}

// EvaluateLifecycle reports the live-object keys eligible for a Delete action
// under the bucket's lifecycle rules. It is non-destructive (real GCS runs the
// sweep asynchronously; ApplyLifecycleGCS performs the destructive pass). When
// a verbatim GCS config is present its full condition set is honored; otherwise
// the portable age/prefix rules are used.
func (m *Mock) EvaluateLifecycle(_ context.Context, bucket string) ([]string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	now := m.opts.Clock.Now().UTC()

	bkt.mu.Lock()
	raw := bkt.gcsLifecycleRaw
	portable := bkt.lifecycle
	bkt.mu.Unlock()

	if len(raw) > 0 {
		return evaluateRichLive(bkt, raw, now), nil
	}

	if portable == nil {
		return nil, nil
	}

	return evaluatePortableLive(bkt, portable, now), nil
}

// evaluateRichLive returns the sorted live keys a Delete rule expires under the
// verbatim GCS lifecycle conditions.
func evaluateRichLive(bkt *bucketMeta, raw []byte, now time.Time) []string {
	var doc gcsLifecycleDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}

	var result []string

	for _, key := range bkt.objects.Keys() {
		obj, objOk := bkt.objects.Get(key)
		if !objOk {
			continue
		}

		if liveObjectExpires(obj, doc.Rule, now) {
			result = append(result, key)
		}
	}

	sort.Strings(result)

	return result
}

// evaluatePortableLive is the age/prefix fallback used when no verbatim GCS
// config is stored (e.g. lifecycle set through the typed PutLifecycleConfig).
func evaluatePortableLive(bkt *bucketMeta, cfg *driver.LifecycleConfig, now time.Time) []string {
	var result []string

	for _, key := range bkt.objects.Keys() {
		obj, objOk := bkt.objects.Get(key)
		if !objOk {
			continue
		}

		if portableObjectExpired(obj, cfg, now) {
			result = append(result, key)
		}
	}

	sort.Strings(result)

	return result
}

func portableObjectExpired(obj *gcsObject, cfg *driver.LifecycleConfig, now time.Time) bool {
	modified, err := time.Parse(gcsTimeFormat, obj.LastModified)
	if err != nil {
		return false
	}

	age := now.Sub(modified)

	for _, rule := range cfg.Rules {
		if !rule.Enabled {
			continue
		}

		if rule.Prefix != "" && !strings.HasPrefix(obj.Key, rule.Prefix) {
			continue
		}

		if rule.ExpirationDays > 0 && age >= daysDuration(rule.ExpirationDays) {
			return true
		}
	}

	return false
}

// liveObjectExpires reports whether a live object matches any Delete rule.
func liveObjectExpires(obj *gcsObject, rules []gcsLifecycleRule, now time.Time) bool {
	for i := range rules {
		if rules[i].Action.Type != actionDelete {
			continue
		}

		if matchesLive(obj, &rules[i].Condition, now) {
			return true
		}
	}

	return false
}

// matchesLive evaluates a rule's conditions against a live object. Conditions
// are ANDed; a condition that only applies to noncurrent versions
// (numNewerVersions / daysSinceNoncurrentTime / noncurrentTimeBefore) or that
// cannot be evaluated for lack of state (customTime) makes the rule non-matching
// for a live object, so the emulator never over-deletes.
func matchesLive(obj *gcsObject, c *gcsLifecycleCondition, now time.Time) bool {
	if c.IsLive != nil && !*c.IsLive {
		return false
	}

	if c.NumNewerVersions != nil || c.DaysSinceNoncurrentTime != nil || c.NoncurrentTimeBefore != "" {
		return false
	}

	return commonConditionsMatch(obj, c, now) && conditionIsEvaluable(c)
}

// matchesNoncurrent evaluates a rule's conditions against a noncurrent version,
// where newer is the number of newer versions of the same object (including the
// live one). numNewerVersions is the versioning-aware condition; other
// conditions are ANDed with it.
func matchesNoncurrent(obj *gcsObject, c *gcsLifecycleCondition, newer int, now time.Time) bool {
	if c.IsLive != nil && *c.IsLive {
		return false
	}

	if c.NumNewerVersions != nil && newer < *c.NumNewerVersions {
		return false
	}

	return commonConditionsMatch(obj, c, now) && conditionIsEvaluable(c)
}

// commonConditionsMatch checks the conditions shared by live and noncurrent
// objects: age, createdBefore, matchesStorageClass, matchesPrefix, matchesSuffix.
func commonConditionsMatch(obj *gcsObject, c *gcsLifecycleCondition, now time.Time) bool {
	created, err := time.Parse(gcsTimeFormat, objectCreated(obj))
	if err != nil {
		return false
	}

	return ageConditionsMatch(created, c, now) && classAndNameMatch(obj, c)
}

func ageConditionsMatch(created time.Time, c *gcsLifecycleCondition, now time.Time) bool {
	if c.Age != nil && now.Sub(created) < daysDuration(*c.Age) {
		return false
	}

	if c.CreatedBefore != "" {
		before, err := time.Parse(time.DateOnly, c.CreatedBefore)
		if err != nil || !created.Before(before) {
			return false
		}
	}

	return true
}

func classAndNameMatch(obj *gcsObject, c *gcsLifecycleCondition) bool {
	if len(c.MatchesStorageClass) > 0 && !containsFold(c.MatchesStorageClass, obj.StorageClass) {
		return false
	}

	if len(c.MatchesPrefix) > 0 && !anyPrefix(obj.Key, c.MatchesPrefix) {
		return false
	}

	if len(c.MatchesSuffix) > 0 && !anySuffix(obj.Key, c.MatchesSuffix) {
		return false
	}

	return true
}

// conditionIsEvaluable reports whether every condition present on the rule is
// one the emulator can evaluate. customTime and noncurrent-time conditions have
// no backing object state (the emulator does not track an object's custom time
// or the instant a version became noncurrent), so a rule carrying any of them is
// treated as non-matching rather than silently ignoring the unmet guard — the
// conservative choice that avoids deleting versions the condition has not
// actually cleared. These fields still round-trip verbatim; only their
// evaluation is suppressed.
func conditionIsEvaluable(c *gcsLifecycleCondition) bool {
	return c.DaysSinceCustomTime == nil && c.CustomTimeBefore == "" &&
		c.DaysSinceNoncurrentTime == nil && c.NoncurrentTimeBefore == ""
}

// ApplyLifecycleGCS performs the destructive lifecycle pass over noncurrent
// object versions: it deletes noncurrent versions matching a Delete rule
// (numNewerVersions and the other version-applicable conditions), the way real
// GCS prunes old versions on a versioned bucket. It returns the deleted version
// identifiers ("key#generation"), sorted. Live objects are left untouched (use
// EvaluateLifecycle to report live expirations). The pass is Clock-driven for
// deterministic tests.
func (m *Mock) ApplyLifecycleGCS(_ context.Context, bucket string) ([]string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	now := m.opts.Clock.Now().UTC()

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if len(bkt.gcsLifecycleRaw) == 0 {
		return nil, nil
	}

	var doc gcsLifecycleDoc
	if err := json.Unmarshal(bkt.gcsLifecycleRaw, &doc); err != nil {
		return nil, nil
	}

	var deleted []string

	for key := range bkt.versions {
		liveExists := bkt.objects.Has(key)
		deleted = append(deleted, pruneKeyVersions(bkt, key, liveExists, doc.Rule, now)...)
	}

	sort.Strings(deleted)

	return deleted, nil
}

// pruneKeyVersions deletes the noncurrent versions of one key that match a
// Delete rule, keeping the survivors in archival order. bkt.mu is held by the
// caller.
func pruneKeyVersions(bkt *bucketMeta, key string, liveExists bool, rules []gcsLifecycleRule, now time.Time) []string {
	versions := bkt.versions[key]
	total := len(versions)

	live := 0
	if liveExists {
		live = 1
	}

	kept := versions[:0:0]

	var deleted []string

	for i, v := range versions {
		// Versions newer than v: the archived ones after it (total-1-i) plus the
		// live version, if any.
		newer := (total - 1 - i) + live
		if noncurrentVersionMatches(v, rules, newer, now) {
			deleted = append(deleted, key+"#"+strconv.FormatInt(v.Generation, 10))

			continue
		}

		kept = append(kept, v)
	}

	if len(kept) == 0 {
		delete(bkt.versions, key)
	} else {
		bkt.versions[key] = kept
	}

	return deleted
}

func noncurrentVersionMatches(obj *gcsObject, rules []gcsLifecycleRule, newer int, now time.Time) bool {
	for i := range rules {
		if rules[i].Action.Type != actionDelete {
			continue
		}

		if matchesNoncurrent(obj, &rules[i].Condition, newer, now) {
			return true
		}
	}

	return false
}

// objectCreated returns the object's creation timestamp, falling back to its
// last-modified stamp when Created is unset (older snapshots / copy paths).
func objectCreated(obj *gcsObject) string {
	if obj.Created != "" {
		return obj.Created
	}

	return obj.LastModified
}

func daysDuration(days int) time.Duration {
	return time.Duration(days) * gcsHoursPerDay * time.Hour
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}

	return false
}

func anyPrefix(key string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}

	return false
}

func anySuffix(key string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(key, s) {
			return true
		}
	}

	return false
}
