// Package ssm provides an in-memory mock implementation of AWS Systems Manager
// (SSM) Parameter Store.
//
// This is the Layer-3 driver implementation. The portable Layer-1 wrapper that
// adds recording/metrics/rate-limiting/error-injection/latency lives in the
// module-root parameterstore package (parameterstore/parameterstore.go).
//
// When a KMS backend is wired (SetKMSCrypto), a SecureString parameter's value
// is encrypted through real KMS: PutParameter stores genuine ciphertext,
// GetParameter/GetParameters with WithDecryption=true return the decrypted
// value, and WithDecryption=false returns the opaque ciphertext blob (matching
// AWS). Its KeyId is recorded and round-tripped (defaulting to alias/aws/ssm),
// and a disabled/deleted key makes a decrypting read fail. String/StringList
// values are never encrypted. Without KMS wired (library fallback) values are
// stored verbatim and WithDecryption is a no-op.
package ssm

import (
	"context"
	"encoding/base64"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// Compile-time check that Mock implements driver.ParameterStore.
var _ driver.ParameterStore = (*Mock)(nil)

// version is a single stored revision of a parameter.
type version struct {
	value          string
	typ            string
	dataType       string
	version        int64
	lastModified   string
	labels         []string
	keyID          string
	allowedPattern string
}

// paramData holds all versions and current metadata for a parameter name.
type paramData struct {
	name        string
	description string
	tier        string
	versions    []*version
	latest      int64
	tags        map[string]string
	mu          sync.RWMutex
}

// KMSCrypto is the KMS seam SSM uses to encrypt SecureString values. Encrypt
// seals a value under a resolved key; Decrypt reverses it, surfacing a
// disabled/deleted key as an error. The kmscrypto.Envelope adapter satisfies it.
type KMSCrypto interface {
	Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// Mock is an in-memory mock implementation of SSM Parameter Store.
type Mock struct {
	params           *memstore.Store[*paramData]
	commands         *memstore.Store[driver.CommandInvocation]
	instanceResolver InstanceResolver
	// kmsCrypto, when wired via SetKMSCrypto, encrypts SecureString values through
	// real KMS. Nil stores them verbatim (library plaintext fallback).
	kmsCrypto KMSCrypto
	opts      *config.Options
}

// SetKMSCrypto wires the KMS backend so SecureString values are encrypted at
// rest and decrypted on read when WithDecryption is set.
func (m *Mock) SetKMSCrypto(c KMSCrypto) {
	m.kmsCrypto = c
}

// sealValue encrypts a SecureString value under keyID, returning a base64
// ciphertext blob (the opaque form AWS returns when WithDecryption is false).
// Non-secure types and the no-KMS fallback return the value unchanged.
func (m *Mock) sealValue(ctx context.Context, effectiveType, keyID, plaintext string) (string, error) {
	if effectiveType != driver.TypeSecureString || m.kmsCrypto == nil {
		return plaintext, nil
	}

	blob, err := m.kmsCrypto.Encrypt(ctx, keyID, []byte(plaintext))
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(blob), nil
}

// revealValue is the read-side inverse of sealValue. A SecureString is decrypted
// only when WithDecryption is set and KMS is wired; otherwise the stored form
// (opaque ciphertext, or plaintext in the fallback) is returned as-is.
func (m *Mock) revealValue(ctx context.Context, v *version, withDecryption bool) (string, error) {
	if v.typ != driver.TypeSecureString || m.kmsCrypto == nil || !withDecryption {
		return v.value, nil
	}

	blob, err := base64.StdEncoding.DecodeString(v.value)
	if err != nil {
		return "", errors.New(errors.InvalidArgument, "invalid encrypted parameter value")
	}

	plaintext, err := m.kmsCrypto.Decrypt(ctx, blob)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// New creates a new SSM Parameter Store mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		params:   memstore.New[*paramData](),
		commands: memstore.New[driver.CommandInvocation](),
		opts:     opts,
	}
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// arn builds the ARN for a parameter. AWS uses "parameter" + the name (which
// begins with "/" for hierarchical names), joined without a separating slash.
func (m *Mock) arn(name string) string {
	return idgen.AWSARN("ssm", m.opts.Region, m.opts.AccountID, "parameter"+ensureLeadingSlash(name))
}

// ensureLeadingSlash normalizes a name so hierarchical ARNs render like
// "parameter/a/b" while flat names render like "parameter/flat".
func ensureLeadingSlash(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}

	return "/" + name
}

// validType reports whether t is one of the parameter types AWS accepts.
func validType(t string) bool {
	switch t {
	case driver.TypeString, driver.TypeStringList, driver.TypeSecureString:
		return true
	default:
		return false
	}
}

// defaultType returns t when it is a recognized type, and String otherwise —
// used only for an omitted (empty) type, which defaults to String. An
// explicitly invalid type is rejected earlier by PutParameter.
func defaultType(t string) string {
	if validType(t) {
		return t
	}

	return driver.TypeString
}

// resolveKeyID validates and resolves the KMS KeyId for a parameter of the
// given effective type. KeyId is only valid for SecureString: supplying it for
// a String/StringList is rejected. An omitted KeyId on a SecureString defaults
// to the AWS-managed key alias/aws/ssm.
func resolveKeyID(effectiveType, keyID string) (string, error) {
	if effectiveType != driver.TypeSecureString {
		if keyID != "" {
			return "", driver.ErrKeyIDOnNonSecure
		}

		return "", nil
	}

	if keyID == "" {
		return driver.DefaultSecureStringKeyID, nil
	}

	return keyID, nil
}

// validateAllowedPattern checks that value satisfies pattern. An empty pattern
// is a no-op. A pattern that is not a valid regexp is rejected, as is a value
// that does not match it — matching real Parameter Store validation.
func validateAllowedPattern(pattern, value string) error {
	if pattern == "" {
		return nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return driver.ErrInvalidAllowedPattern
	}

	if !re.MatchString(value) {
		return driver.ErrValuePatternMismatch
	}

	return nil
}

// resolveOverwriteType decides the type of a new version appended to an existing
// parameter. A type isn't required when updating: omitting it retains the
// existing type (a SecureString stays SecureString). Specifying a type that
// differs from the existing one is rejected — real Parameter Store returns
// HierarchyTypeMismatchException.
func resolveOverwriteType(existing *paramData, requested string) (string, error) {
	existingType := defaultType("")
	if cur, ok := existing.versionByNumber(existing.latest); ok {
		existingType = cur.typ
	}

	if requested == "" {
		return existingType, nil
	}

	if newType := defaultType(requested); newType == existingType {
		return newType, nil
	}

	return "", driver.ErrTypeMismatch
}

// PutParameter creates a new parameter or, when Overwrite is set, appends a new
// version to an existing one.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PutParameter(ctx context.Context, cfg driver.PutConfig) (int64, string, error) {
	if cfg.Name == "" {
		return 0, "", errors.New(errors.InvalidArgument, "parameter name is required")
	}

	if cfg.Overwrite && len(cfg.Tags) > 0 {
		return 0, "", driver.ErrTagsWithOverwrite
	}

	// An explicitly set Type must be one AWS recognizes; an unrecognized value
	// is rejected (UnsupportedParameterType) rather than coerced to String. An
	// omitted Type is allowed here: it defaults to String on create and retains
	// the existing type on Overwrite.
	if cfg.Type != "" && !validType(cfg.Type) {
		return 0, "", driver.ErrUnsupportedType
	}

	// AllowedPattern (if set) must be a valid regexp and the Value must match it.
	// This is independent of the parameter type, so validate it up front.
	if err := validateAllowedPattern(cfg.AllowedPattern, cfg.Value); err != nil {
		return 0, "", err
	}

	tier := cfg.Tier
	if tier == "" {
		tier = "Standard"
	}

	dataType := cfg.DataType
	if dataType == "" {
		dataType = "text"
	}

	now := m.now()

	if existing, ok := m.params.Get(cfg.Name); ok {
		return m.overwriteParameter(ctx, existing, &cfg, tier, dataType, now)
	}

	return m.createParameter(ctx, &cfg, tier, dataType, now)
}

// overwriteParameter appends a new version to an existing parameter (the
// Overwrite path). Overwrite without the flag is rejected, and changing the
// type is rejected via resolveOverwriteType.
func (m *Mock) overwriteParameter(
	ctx context.Context, existing *paramData, cfg *driver.PutConfig, tier, dataType, now string,
) (ver int64, assignedTier string, err error) {
	existing.mu.Lock()
	defer existing.mu.Unlock()

	if !cfg.Overwrite {
		return 0, "", errors.Newf(errors.AlreadyExists,
			"parameter %q already exists; set Overwrite to update it", cfg.Name)
	}

	newType, err := resolveOverwriteType(existing, cfg.Type)
	if err != nil {
		return 0, "", err
	}

	keyID, err := resolveKeyID(newType, cfg.KeyID)
	if err != nil {
		return 0, "", err
	}

	storedValue, err := m.sealValue(ctx, newType, keyID, cfg.Value)
	if err != nil {
		return 0, "", err
	}

	next := existing.latest + 1
	existing.versions = append(existing.versions, &version{
		value:          storedValue,
		typ:            newType,
		dataType:       dataType,
		version:        next,
		lastModified:   now,
		keyID:          keyID,
		allowedPattern: cfg.AllowedPattern,
	})
	existing.latest = next
	existing.description = cfg.Description
	existing.tier = tier

	return next, tier, nil
}

// createParameter stores a brand-new parameter (version 1) with its tags.
func (m *Mock) createParameter(
	ctx context.Context, cfg *driver.PutConfig, tier, dataType, now string,
) (ver int64, assignedTier string, err error) {
	newType := defaultType(cfg.Type)

	keyID, err := resolveKeyID(newType, cfg.KeyID)
	if err != nil {
		return 0, "", err
	}

	storedValue, err := m.sealValue(ctx, newType, keyID, cfg.Value)
	if err != nil {
		return 0, "", err
	}

	pd := &paramData{
		name:        cfg.Name,
		description: cfg.Description,
		tier:        tier,
		latest:      1,
		versions: []*version{{
			value:          storedValue,
			typ:            newType,
			dataType:       dataType,
			version:        1,
			lastModified:   now,
			keyID:          keyID,
			allowedPattern: cfg.AllowedPattern,
		}},
		tags: copyTags(cfg.Tags),
	}

	// SetIfAbsent guards against a concurrent create racing between Get and Set.
	if !m.params.SetIfAbsent(cfg.Name, pd) {
		// Lost the race: retry as an overwrite path only if allowed.
		if !cfg.Overwrite {
			return 0, "", errors.Newf(errors.AlreadyExists,
				"parameter %q already exists; set Overwrite to update it", cfg.Name)
		}

		cfg.Overwrite = true

		return m.PutParameter(ctx, *cfg)
	}

	return 1, tier, nil
}

// resolveSelector splits a name of the form "name:selector" into its base name
// and selector (a version number or a label). An empty selector means latest.
func resolveSelector(name string) (base, selector string) {
	// Hierarchical names contain slashes but never a colon in the path itself,
	// so the last colon (if any) introduces a version/label selector.
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[:i], name[i+1:]
	}

	return name, ""
}

// pick returns the version matching the selector (empty = latest, numeric =
// version, otherwise a label). Callers must hold pd.mu.
func (pd *paramData) pick(selector string) (*version, bool) {
	if selector == "" {
		return pd.versionByNumber(pd.latest)
	}

	if n, err := strconv.ParseInt(selector, 10, 64); err == nil {
		return pd.versionByNumber(n)
	}

	for _, v := range pd.versions {
		for _, l := range v.labels {
			if l == selector {
				return v, true
			}
		}
	}

	return nil, false
}

func (pd *paramData) versionByNumber(n int64) (*version, bool) {
	for _, v := range pd.versions {
		if v.version == n {
			return v, true
		}
	}

	return nil, false
}

func (m *Mock) toParameter(
	ctx context.Context, pd *paramData, v *version, selector string, withDecryption bool,
) (driver.Parameter, error) {
	name := pd.name
	if selector != "" {
		name = pd.name + ":" + selector
	}

	value, err := m.revealValue(ctx, v, withDecryption)
	if err != nil {
		return driver.Parameter{}, err
	}

	return driver.Parameter{
		Name:         pd.name,
		Type:         v.typ,
		Value:        value,
		Version:      v.version,
		ARN:          m.arn(pd.name),
		DataType:     v.dataType,
		LastModified: v.lastModified,
		Selector:     selectorFor(name, pd.name),
	}, nil
}

func selectorFor(requested, base string) string {
	if requested == base {
		return ""
	}

	return strings.TrimPrefix(requested, base+":")
}

// GetParameter retrieves a single parameter by name, honoring an optional
// ":version" or ":label" selector suffix. For a SecureString, withDecryption=true
// returns the decrypted value; false returns the opaque ciphertext blob.
func (m *Mock) GetParameter(ctx context.Context, name string, withDecryption bool) (*driver.Parameter, error) {
	base, selector := resolveSelector(name)

	// AWS-published parameters are readable from every account without having
	// been put, so resolving one must not answer NotFound.
	m.ensurePublicParameter(base)

	pd, ok := m.params.Get(base)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q not found", base)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	v, ok := pd.pick(selector)
	if !ok {
		// Parameter exists but this version/label doesn't — distinct from the
		// parameter being absent, so the handler can return the specific
		// ParameterVersionNotFound wire error.
		return nil, driver.ErrVersionNotFound
	}

	p, err := m.toParameter(ctx, pd, v, selector, withDecryption)
	if err != nil {
		return nil, err
	}

	return &p, nil
}

// GetParameters retrieves multiple parameters, reporting names that were not
// found (or whose selector did not resolve) as invalid rather than erroring.
func (m *Mock) GetParameters(
	ctx context.Context, names []string, withDecryption bool,
) ([]driver.Parameter, []string, error) {
	found := make([]driver.Parameter, 0, len(names))

	var invalid []string

	for _, name := range names {
		base, selector := resolveSelector(name)

		m.ensurePublicParameter(base)

		pd, ok := m.params.Get(base)
		if !ok {
			invalid = append(invalid, name)
			continue
		}

		pd.mu.RLock()
		v, ok := pd.pick(selector)
		if !ok {
			pd.mu.RUnlock()

			invalid = append(invalid, name)

			continue
		}

		p, err := m.toParameter(ctx, pd, v, selector, withDecryption)
		pd.mu.RUnlock()

		if err != nil {
			return nil, nil, err
		}

		found = append(found, p)
	}

	return found, invalid, nil
}

// GetParametersByPath returns the latest version of every parameter under a
// hierarchical path. With Recursive false, only direct children are returned;
// with Recursive true, the whole subtree is returned.
func (m *Mock) GetParametersByPath(ctx context.Context, in driver.GetByPathInput) ([]driver.Parameter, error) {
	path := in.Path
	if path == "" {
		path = "/"
	}

	if !strings.HasPrefix(path, "/") {
		return nil, errors.New(errors.InvalidArgument, "path must begin with '/'")
	}

	if err := validateByPathFilters(in.ParameterFilters); err != nil {
		return nil, err
	}

	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var out []driver.Parameter

	for _, pd := range m.params.All() {
		p, matched, err := m.pathParameter(ctx, pd, prefix, &in)
		if err != nil {
			return nil, err
		}

		if matched {
			out = append(out, p)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// pathParameter renders pd's latest version for a GetParametersByPath result,
// reporting matched=false when pd is out of the path's scope or filtered out.
// SecureString values are decrypted only when in.WithDecryption is set.
func (m *Mock) pathParameter(
	ctx context.Context, pd *paramData, prefix string, in *driver.GetByPathInput,
) (param driver.Parameter, matched bool, err error) {
	if !strings.HasPrefix(pd.name, prefix) {
		return driver.Parameter{}, false, nil
	}

	// Non-recursive: only direct children (no further "/" in the remainder).
	if !in.Recursive && strings.Contains(strings.TrimPrefix(pd.name, prefix), "/") {
		return driver.Parameter{}, false, nil
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	v, ok := pd.versionByNumber(pd.latest)
	if !ok || !matchesByPathFilters(v, in.ParameterFilters) {
		return driver.Parameter{}, false, nil
	}

	param, err = m.toParameter(ctx, pd, v, "", in.WithDecryption)
	if err != nil {
		return driver.Parameter{}, false, err
	}

	return param, true, nil
}

// DeleteParameter removes a parameter and all its versions. A ":version"/
// ":label" selector is stripped first, matching the read paths — SSM has no
// per-version delete, so a selector addresses the base parameter.
func (m *Mock) DeleteParameter(_ context.Context, name string) error {
	base, _ := resolveSelector(name)
	if !m.params.Delete(base) {
		return errors.Newf(errors.NotFound, "parameter %q not found", base)
	}

	return nil
}

// DeleteParameters removes multiple parameters, returning the names deleted and
// the names that did not exist.
func (m *Mock) DeleteParameters(_ context.Context, names []string) ([]string, []string, error) {
	var deleted, invalid []string

	for _, name := range names {
		base, _ := resolveSelector(name)
		if m.params.Delete(base) {
			deleted = append(deleted, name)
		} else {
			invalid = append(invalid, name)
		}
	}

	return deleted, invalid, nil
}

// DescribeParameters lists metadata (no values) for all parameters.
func (m *Mock) DescribeParameters(_ context.Context) ([]driver.ParameterMetadata, error) {
	all := m.params.All()

	out := make([]driver.ParameterMetadata, 0, len(all))

	for _, pd := range all {
		pd.mu.RLock()
		if v, ok := pd.versionByNumber(pd.latest); ok {
			out = append(out, driver.ParameterMetadata{
				Name:             pd.name,
				Type:             v.typ,
				Description:      pd.description,
				Version:          pd.latest,
				ARN:              m.arn(pd.name),
				Tier:             pd.tier,
				DataType:         v.dataType,
				LastModified:     v.lastModified,
				LastModifiedUser: idgen.AWSARN("iam", "", m.opts.AccountID, "user/cloudemu"),
				KeyID:            v.keyID,
				AllowedPattern:   v.allowedPattern,
			})
		}
		pd.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// GetParameterHistory returns every version of a parameter, oldest first.
func (m *Mock) GetParameterHistory(_ context.Context, name string) ([]driver.Parameter, error) {
	pd, ok := m.params.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	lastModifiedUser := idgen.AWSARN("iam", "", m.opts.AccountID, "user/cloudemu")

	out := make([]driver.Parameter, 0, len(pd.versions))
	for _, v := range pd.versions {
		labels := append([]string(nil), v.labels...)
		out = append(out, driver.Parameter{
			Name:             pd.name,
			Type:             v.typ,
			Value:            v.value,
			Version:          v.version,
			ARN:              m.arn(pd.name),
			DataType:         v.dataType,
			LastModified:     v.lastModified,
			Labels:           labels,
			Description:      pd.description,
			Tier:             pd.tier,
			LastModifiedUser: lastModifiedUser,
			KeyID:            v.keyID,
			AllowedPattern:   v.allowedPattern,
		})
	}

	return out, nil
}

// LabelParameterVersion attaches labels to a specific version (0 = latest),
// returning the version the labels were applied to and any labels rejected.
// A label attached to a new version is removed from any older version that held
// it, matching real SSM semantics.
func (m *Mock) LabelParameterVersion(_ context.Context, name string, ver int64, labels []string) (int64, []string, error) {
	pd, ok := m.params.Get(name)
	if !ok {
		return 0, nil, errors.Newf(errors.NotFound, "parameter %q not found", name)
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	if ver == 0 {
		ver = pd.latest
	}

	target, ok := pd.versionByNumber(ver)
	if !ok {
		return 0, nil, errors.Newf(errors.NotFound, "parameter %q version %d not found", name, ver)
	}

	var invalid []string

	for _, label := range labels {
		// Real SSM rejects labels that begin with a digit or with "aws"/"ssm"
		// (case-insensitive). Rejecting them here also prevents a numeric label
		// like "5" from being attached and then shadowed by version-number
		// resolution in pick().
		if !validLabel(label) {
			invalid = append(invalid, label)
			continue
		}

		// Detach the label from any other version first.
		for _, v := range pd.versions {
			if v == target {
				continue
			}

			v.labels = removeString(v.labels, label)
		}

		if !containsString(target.labels, label) {
			target.labels = append(target.labels, label)
		}
	}

	return ver, invalid, nil
}

// validLabel reports whether a parameter label is acceptable to real SSM: it
// must be non-empty, must not start with a digit, and must not start with
// "aws" or "ssm" (case-insensitive).
func validLabel(label string) bool {
	if label == "" {
		return false
	}

	if label[0] >= '0' && label[0] <= '9' {
		return false
	}

	lower := strings.ToLower(label)

	return !strings.HasPrefix(lower, "aws") && !strings.HasPrefix(lower, "ssm")
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}

	return false
}

func removeString(ss []string, s string) []string {
	out := ss[:0]

	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}

	return out
}
