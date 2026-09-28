package backupdr

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

const (
	stateActive = "ACTIVE"

	accessWithinOrganization = "WITHIN_ORGANIZATION"
	accessUnspecified        = "ACCESS_RESTRICTION_UNSPECIFIED"

	// minRetention / maxRetention bound backupMinimumEnforcedRetentionDuration:
	// "The minimum is 1 day and the maximum is 99 years" (Backup and DR "Create
	// a backup vault"). 99 years is taken as 99 x 365.25 days so no calendar
	// reading of "99 years" is rejected.
	minRetention = 24 * time.Hour
	maxRetention = 36159*24*time.Hour + 18*time.Hour

	// serviceAccountDomain is the Backup and DR service-agent domain. CloudEmu
	// synthesizes the vault serviceAccount as
	// service-{projectNumber}@gcp-sa-backupdr-pr.iam.gserviceaccount.com, where
	// projectNumber is a stable 12-digit number derived from the project id (see
	// projectNumber), so it is identical on every read and across restarts.
	serviceAccountDomain = "@gcp-sa-backupdr-pr.iam.gserviceaccount.com"

	// projectNumberBase / projectNumberSpan keep a derived project number at
	// exactly 12 digits (100000000000..999999999999), like a real GCP project
	// number.
	projectNumberBase = 100000000000
	projectNumberSpan = 900000000000

	fieldDescription      = "description"
	fieldLabels           = "labels"
	fieldAnnotations      = "annotations"
	fieldRetention        = "backupMinimumEnforcedRetentionDuration"
	fieldInheritance      = "backupRetentionInheritance"
	fieldEffectiveTime    = "effectiveTime"
	fieldAccess           = "accessRestriction"
	fieldEncryptionCfg    = "encryptionConfig"
	etagRevisionSeparator = "#"

	// hexBase / decimalBase are the strconv bases for the etag and numbers.
	hexBase     = 16
	decimalBase = 10
)

// vaultIDPattern is the documented backup vault name rule: only lowercase
// letters, digits and hyphens, starting and ending with a letter or digit, 3-63
// characters (Backup and DR "Backup vaults", name requirements). It also keeps
// '/' out of an id, which would otherwise mint an unreachable resource name.
var vaultIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// durationPattern matches the google.protobuf.Duration JSON form: an optionally
// signed decimal number of seconds with up to nine fractional digits and an "s"
// suffix (e.g. "86400s", "1.5s", "-3s").
var durationPattern = regexp.MustCompile(`^(-)?\d+(\.\d{1,9})?s$`)

// validAccessRestrictions is the AccessRestriction enum from the discovery doc.
//
//nolint:gochecknoglobals // immutable lookup set
var validAccessRestrictions = map[string]bool{
	accessUnspecified:                    true,
	"WITHIN_PROJECT":                     true,
	accessWithinOrganization:             true,
	"UNRESTRICTED":                       true,
	"WITHIN_ORG_BUT_UNRESTRICTED_FOR_BA": true,
}

// validInheritance is the BackupRetentionInheritance enum from the discovery doc.
//
//nolint:gochecknoglobals // immutable lookup set
var validInheritance = map[string]bool{
	"BACKUP_RETENTION_INHERITANCE_UNSPECIFIED": true,
	"INHERIT_VAULT_RETENTION":                  true,
	"MATCH_BACKUP_EXPIRE_TIME":                 true,
}

// outputOnlyFields are BackupVault fields a caller may not name in an
// updateMask: they are output-only (or, for name, immutable identity).
//
//nolint:gochecknoglobals // immutable lookup set
var outputOnlyFields = map[string]bool{
	"name": true, "createTime": true, "updateTime": true, "state": true,
	"deletable": true, "etag": true, "serviceAccount": true, "uid": true,
	"totalStoredBytes": true, "backupCount": true,
}

// mutableFields are the BackupVault fields an updateMask may name.
//
//nolint:gochecknoglobals // immutable lookup set
var mutableFields = map[string]bool{
	fieldDescription: true, fieldLabels: true, fieldAnnotations: true, fieldRetention: true,
	fieldInheritance: true, fieldEffectiveTime: true, fieldAccess: true, fieldEncryptionCfg: true,
}

// validateCreate checks a create request: the vault id and the required
// retention duration, plus every optional enum/timestamp the caller supplied.
func validateCreate(cfg *bdrdriver.BackupVaultConfig) error {
	if cfg.Location == "" || cfg.Location == anyLocation {
		return cerrors.New(cerrors.InvalidArgument, "a concrete location is required")
	}

	if !vaultIDPattern.MatchString(cfg.ID) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"backupVaultId %q must be 3-63 characters of lowercase letters, digits and hyphens, "+
				"starting and ending with a letter or digit", cfg.ID)
	}

	checks := []func(*bdrdriver.BackupVaultConfig) error{
		checkRetention, checkInheritance, checkEffectiveTime, checkAccessRestriction,
	}

	for _, check := range checks {
		if err := check(cfg); err != nil {
			return err
		}
	}

	return nil
}

// checkRetention requires backupMinimumEnforcedRetentionDuration to be a
// well-formed google.protobuf.Duration string between 1 day and 99 years.
func checkRetention(cfg *bdrdriver.BackupVaultConfig) error {
	d := cfg.BackupMinimumEnforcedRetentionDuration
	if d == "" {
		return cerrors.New(cerrors.InvalidArgument, "backupMinimumEnforcedRetentionDuration is required")
	}

	got, err := parseRetention(d)
	if err != nil {
		return err
	}

	if got < minRetention || got > maxRetention {
		return cerrors.Newf(cerrors.InvalidArgument,
			"backupMinimumEnforcedRetentionDuration %q must be between 1 day (86400s) and 99 years", d)
	}

	return nil
}

// parseRetention parses a google.protobuf.Duration JSON string ("86400s").
func parseRetention(d string) (time.Duration, error) {
	if !durationPattern.MatchString(d) {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"backupMinimumEnforcedRetentionDuration %q is not a valid duration (want e.g. \"86400s\")", d)
	}

	got, err := time.ParseDuration(d)
	if err != nil {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"backupMinimumEnforcedRetentionDuration %q is out of range", d)
	}

	return got, nil
}

// lockedAt reports whether a vault's retention lock is in effect at now: an
// effectiveTime is set and has been reached. The stored value was validated as
// RFC 3339 on write.
func lockedAt(v *bdrdriver.BackupVault, now time.Time) bool {
	if v.EffectiveTime == "" {
		return false
	}

	t, err := time.Parse(time.RFC3339Nano, v.EffectiveTime)

	return err == nil && !now.Before(t)
}

// checkLock enforces the retention lock on a masked update. Before the
// effective time a vault's retention and lock time may change freely; once it
// has passed, "no one (not even a Project Owner) can decrease the retention
// period. You are only permitted to increase it", and the lock "cannot be
// removed if the effective date has been reached", so effectiveTime is frozen.
// Both rejections are FAILED_PRECONDITION: the request is well-formed, the
// vault's state forbids it.
func checkLock(v *bdrdriver.BackupVault, cfg *bdrdriver.BackupVaultConfig, fields map[string]bool, now time.Time) error {
	if !lockedAt(v, now) {
		return nil
	}

	name := resourceName(v.Project, v.Location, v.ID)

	if fields[fieldEffectiveTime] && cfg.EffectiveTime != v.EffectiveTime {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"backup vault %q is locked since %s; effectiveTime cannot be changed", name, v.EffectiveTime)
	}

	if !fields[fieldRetention] {
		return nil
	}

	// A stored value that does not parse (never written by this mock) compares
	// as zero, so any valid new value is an increase.
	cur, _ := parseRetention(v.BackupMinimumEnforcedRetentionDuration)

	next, err := parseRetention(cfg.BackupMinimumEnforcedRetentionDuration)
	if err != nil {
		return err
	}

	if next < cur {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"backup vault %q is locked since %s; backupMinimumEnforcedRetentionDuration can only be increased (currently %s)",
			name, v.EffectiveTime, v.BackupMinimumEnforcedRetentionDuration)
	}

	return nil
}

// checkInheritance validates an optional backupRetentionInheritance enum.
func checkInheritance(cfg *bdrdriver.BackupVaultConfig) error {
	if v := cfg.BackupRetentionInheritance; v != "" && !validInheritance[v] {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid backupRetentionInheritance %q", v)
	}

	return nil
}

// checkEffectiveTime validates an optional RFC 3339 effectiveTime.
func checkEffectiveTime(cfg *bdrdriver.BackupVaultConfig) error {
	if v := cfg.EffectiveTime; v != "" {
		if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "effectiveTime %q is not an RFC 3339 timestamp", v)
		}
	}

	return nil
}

// checkAccessRestriction validates an optional accessRestriction enum.
func checkAccessRestriction(cfg *bdrdriver.BackupVaultConfig) error {
	if v := cfg.AccessRestriction; v != "" && !validAccessRestrictions[v] {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid accessRestriction %q", v)
	}

	return nil
}

// defaultAccessRestriction applies the documented default: an absent or
// UNSPECIFIED access restriction becomes WITHIN_ORGANIZATION.
func defaultAccessRestriction(v string) string {
	if v == "" || v == accessUnspecified {
		return accessWithinOrganization
	}

	return v
}

// normalizeMask validates a required updateMask and returns the set of
// top-level camelCase fields it names. Paths may be snake_case (the proto
// spelling some clients send) or camelCase; a nested path
// (encryptionConfig.kmsKeyName) names its top-level field. An empty mask, an
// output-only field, or an unknown field is INVALID_ARGUMENT.
func normalizeMask(mask []string) (map[string]bool, error) {
	if len(mask) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "updateMask is required")
	}

	out := make(map[string]bool, len(mask))

	for _, path := range mask {
		top := snakeToCamel(strings.SplitN(path, ".", 2)[0]) //nolint:mnd // split into head and rest

		switch {
		case outputOnlyFields[top]:
			return nil, cerrors.Newf(cerrors.InvalidArgument, "updateMask path %q names an output-only field", path)
		case !mutableFields[top]:
			return nil, cerrors.Newf(cerrors.InvalidArgument, "updateMask path %q is not a BackupVault field", path)
		}

		out[top] = true
	}

	return out, nil
}

// applyMask copies each masked field from cfg onto v, validating the new value
// and the retention lock as of now. Fields outside the mask are left untouched.
func applyMask(v *bdrdriver.BackupVault, cfg *bdrdriver.BackupVaultConfig, fields map[string]bool, now time.Time) error {
	checks := map[string]func(*bdrdriver.BackupVaultConfig) error{
		fieldRetention:     checkRetention,
		fieldInheritance:   checkInheritance,
		fieldEffectiveTime: checkEffectiveTime,
		fieldAccess:        checkAccessRestriction,
	}

	for f := range fields {
		if check, ok := checks[f]; ok {
			if err := check(cfg); err != nil {
				return err
			}
		}
	}

	if err := checkLock(v, cfg, fields, now); err != nil {
		return err
	}

	setters := map[string]func(){

		fieldDescription:   func() { v.Description = cfg.Description },
		fieldLabels:        func() { v.Labels = cloneStrMap(cfg.Labels) },
		fieldAnnotations:   func() { v.Annotations = cloneStrMap(cfg.Annotations) },
		fieldRetention:     func() { v.BackupMinimumEnforcedRetentionDuration = cfg.BackupMinimumEnforcedRetentionDuration },
		fieldInheritance:   func() { v.BackupRetentionInheritance = cfg.BackupRetentionInheritance },
		fieldEffectiveTime: func() { v.EffectiveTime = cfg.EffectiveTime },
		fieldAccess:        func() { v.AccessRestriction = defaultAccessRestriction(cfg.AccessRestriction) },
		fieldEncryptionCfg: func() { v.EncryptionConfig = cloneEncryption(cfg.EncryptionConfig) },
	}

	for f := range fields {
		setters[f]()
	}

	return nil
}

// snakeToCamel converts a snake_case proto field name to its JSON camelCase
// spelling; a name without underscores is returned unchanged.
func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}

	parts := strings.Split(s, "_")

	var b strings.Builder

	b.WriteString(parts[0])

	for _, p := range parts[1:] {
		if p == "" {
			continue
		}

		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}

	return b.String()
}

// projectNumber derives a stable 12-digit project number from a project id, so
// the synthesized service account is deterministic.
func projectNumber(project string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(project))

	return projectNumberBase + h.Sum64()%projectNumberSpan
}

// serviceAccount returns the deterministic Backup and DR service agent for a
// project (see serviceAccountDomain).
func serviceAccount(project string) string {
	return "service-" + strconv.FormatUint(projectNumber(project), decimalBase) + serviceAccountDomain
}

// etagFor derives the vault etag from its identity, revision and update time,
// so it changes on every successful update and is deterministic under a fake
// clock.
func etagFor(v *bdrdriver.BackupVault) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(resourceName(v.Project, v.Location, v.ID) + etagRevisionSeparator +
		strconv.FormatInt(v.Revision, decimalBase) + etagRevisionSeparator + v.UpdateTime.Format(time.RFC3339Nano)))

	return strconv.FormatUint(h.Sum64(), hexBase)
}
