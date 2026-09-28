package loadbalancer

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Cloud CDN limits, from the BackendBucketCdnPolicy field docs in
// cloud.google.com/go/compute/apiv1/computepb (compute v1.60.0).
const (
	// maxCDNTTLSeconds is the largest defaultTtl / maxTtl / clientTtl (1 year).
	maxCDNTTLSeconds = 31622400
	// maxServeWhileStaleSeconds is the largest serveWhileStale (1 week).
	maxServeWhileStaleSeconds = 604800
	// maxBypassCacheHeaders is how many bypassCacheOnRequestHeaders are allowed.
	maxBypassCacheHeaders = 5

	cacheModeCacheAllStatic   = "CACHE_ALL_STATIC"
	cacheModeUseOriginHeaders = "USE_ORIGIN_HEADERS"
	cacheModeForceCacheAll    = "FORCE_CACHE_ALL"

	// defaultCDNTTL is the documented defaultTtl and clientTtl (1 hour), and
	// defaultCDNMaxTTL the documented maxTtl (1 day), for a mode that uses them.
	defaultCDNTTL    = 3600
	defaultCDNMaxTTL = 86400

	fieldCacheMode  = "cacheMode"
	fieldDefaultTTL = "defaultTtl"
	fieldMaxTTL     = "maxTtl"
	fieldClientTTL  = "clientTtl"

	fieldService        = "service"
	fieldDefaultService = "defaultService"
)

// rfc1035Name is the compute resource-name grammar: 1-63 characters, a
// lowercase letter first, then lowercase letters, digits or dashes, not ending
// in a dash.
var rfc1035Name = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validCacheModes are the cdnPolicy.cacheMode values the API accepts.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var validCacheModes = map[string]bool{
	cacheModeCacheAllStatic:   true,
	cacheModeUseOriginHeaders: true,
	cacheModeForceCacheAll:    true,
}

// validCompressionModes are the compressionMode values the API accepts.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var validCompressionModes = map[string]bool{
	"AUTOMATIC": true,
	"DISABLED":  true,
}

// urlMapServiceFields are the url-map members that name a backend service or
// backend bucket.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var urlMapServiceFields = map[string]bool{fieldService: true, fieldDefaultService: true}

// isBackendBucketRef reports whether ref is a backendBuckets self-link or
// relative path (full URL or "projects/{p}/global/backendBuckets/{name}").
func isBackendBucketRef(ref string) bool {
	return strings.Contains(ref, "/"+resourceBackendBuckets+"/")
}

// validateRFC1035Name rejects a missing or malformed resource name.
func validateRFC1035Name(name string) error {
	if !rfc1035Name.MatchString(name) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.name': '%s'. Must be a match of regex '(?:[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?)'", name)
	}

	return nil
}

// applyBackendBucketDefaults fills cdnPolicy.cacheMode with the documented
// default (CACHE_ALL_STATIC) when Cloud CDN is enabled or a cdnPolicy is given
// without one, then the TTLs the chosen mode uses (applyCDNTTLDefaults).
func applyBackendBucketDefaults(body map[string]any) {
	enabled, _ := body["enableCdn"].(bool)
	policy, hasPolicy := body["cdnPolicy"].(map[string]any)

	if !enabled && !hasPolicy {
		return
	}

	if !hasPolicy {
		policy = map[string]any{}
		body["cdnPolicy"] = policy
	}

	if _, ok := policy[fieldCacheMode]; !ok {
		policy[fieldCacheMode] = cacheModeCacheAllStatic
	}

	applyCDNTTLDefaults(policy)
}

// applyCDNTTLDefaults fills the TTLs GCP reports for a cacheMode when the
// caller left them out: CACHE_ALL_STATIC gets defaultTtl 3600, maxTtl 86400 and
// clientTtl 3600; FORCE_CACHE_ALL gets defaultTtl and clientTtl 3600 (it has no
// maxTtl); USE_ORIGIN_HEADERS gets none, because it takes every TTL from the
// origin. A filled defaultTtl/clientTtl never exceeds an explicit smaller
// maxTtl, so a default can never be the reason a request is refused.
func applyCDNTTLDefaults(policy map[string]any) {
	mode, _ := policy[fieldCacheMode].(string)
	if mode == cacheModeUseOriginHeaders {
		return
	}

	limit := int64(math.MaxInt64)

	if mode == cacheModeCacheAllStatic {
		if _, ok := policy[fieldMaxTTL]; !ok {
			policy[fieldMaxTTL] = float64(defaultCDNMaxTTL)
		}

		if m, ok := jsonInt(policy[fieldMaxTTL]); ok {
			limit = m
		}
	}

	for _, f := range []string{fieldDefaultTTL, fieldClientTTL} {
		if _, ok := policy[f]; !ok {
			policy[f] = float64(min(defaultCDNTTL, limit))
		}
	}
}

// dropTTLsForbiddenByMode removes, from a merge-patched cdnPolicy, the TTLs the
// new cacheMode forbids that the patch itself did not send. Switching a stored
// CACHE_ALL_STATIC policy (which carries defaulted TTLs) to USE_ORIGIN_HEADERS
// must not be refused over TTLs the caller never chose; a TTL the patch does
// send is still validated.
func dropTTLsForbiddenByMode(next, patch map[string]any) {
	patchPolicy, _ := patch["cdnPolicy"].(map[string]any)
	if _, changed := patchPolicy[fieldCacheMode]; !changed {
		return
	}

	policy, _ := next["cdnPolicy"].(map[string]any)
	mode, _ := policy[fieldCacheMode].(string)

	for _, f := range cdnTTLsForbiddenBy(mode) {
		if _, sent := patchPolicy[f]; !sent {
			delete(policy, f)
		}
	}
}

// cdnTTLsForbiddenBy lists the TTL fields a cacheMode refuses a non-zero value
// for: USE_ORIGIN_HEADERS takes every TTL from the origin, and FORCE_CACHE_ALL
// caches for defaultTtl so it has no maxTtl.
func cdnTTLsForbiddenBy(mode string) []string {
	switch mode {
	case cacheModeUseOriginHeaders:
		return []string{fieldDefaultTTL, fieldMaxTTL, fieldClientTTL}
	case cacheModeForceCacheAll:
		return []string{fieldMaxTTL}
	default:
		return nil
	}
}

// validateCompressionMode rejects an unrecognized compressionMode.
func validateCompressionMode(v any) error {
	if v == nil {
		return nil
	}

	mode, _ := v.(string)
	if !validCompressionModes[mode] {
		return cerrors.Newf(cerrors.InvalidArgument, "Invalid value for field 'resource.compressionMode': '%v'.", v)
	}

	return nil
}

// validateCDNPolicy checks the cdnPolicy members with documented constraints;
// every other member passes through untouched.
func validateCDNPolicy(v any) error {
	if v == nil {
		return nil
	}

	policy, ok := v.(map[string]any)
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "Invalid value for field 'resource.cdnPolicy': must be an object.")
	}

	if mode, present := policy[fieldCacheMode]; present {
		if s, _ := mode.(string); !validCacheModes[s] {
			return cerrors.Newf(cerrors.InvalidArgument, "Invalid value for field 'resource.cdnPolicy.cacheMode': '%v'.", mode)
		}
	}

	if err := validateCDNRanges(policy); err != nil {
		return err
	}

	return validateCDNLists(policy)
}

// validateCDNRanges checks the TTL bounds, then the cross-field TTL rules.
func validateCDNRanges(policy map[string]any) error {
	limits := []struct {
		field string
		max   int64
	}{
		{fieldDefaultTTL, maxCDNTTLSeconds},
		{fieldMaxTTL, maxCDNTTLSeconds},
		{fieldClientTTL, maxCDNTTLSeconds},
		{"serveWhileStale", maxServeWhileStaleSeconds},
		{"signedUrlCacheMaxAgeSec", math.MaxInt64},
	}

	for _, l := range limits {
		raw, present := policy[l.field]
		if !present {
			continue
		}

		n, ok := jsonInt(raw)
		if !ok || n < 0 || n > l.max {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.cdnPolicy.%s': '%v'. Must be between 0 and %d.", l.field, raw, l.max)
		}
	}

	return validateCDNModeTTLs(policy)
}

// validateCDNModeTTLs applies the cross-field TTL rules: a cacheMode refuses a
// non-zero TTL it does not use (cdnTTLsForbiddenBy), and under
// CACHE_ALL_STATIC both defaultTtl and clientTtl are capped by the effective
// maxTtl — the explicit one, or the 86400 default when it is unset.
func validateCDNModeTTLs(policy map[string]any) error {
	mode, _ := policy[fieldCacheMode].(string)

	for _, f := range cdnTTLsForbiddenBy(mode) {
		if n, ok := jsonInt(policy[f]); ok && n != 0 {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.cdnPolicy.%s': '%d'. %s cannot be specified with the %s cache mode.",
				f, n, f, mode)
		}
	}

	if mode != cacheModeCacheAllStatic && mode != "" {
		return nil
	}

	maxTTL, hasMax := jsonInt(policy[fieldMaxTTL])
	if !hasMax {
		maxTTL = defaultCDNMaxTTL
	}

	for _, f := range []string{fieldDefaultTTL, fieldClientTTL} {
		if n, ok := jsonInt(policy[f]); ok && n > maxTTL {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value for field 'resource.cdnPolicy.%s': '%d'. %s cannot be greater than maxTtl (%d).",
				f, n, f, maxTTL)
		}
	}

	return nil
}

// validateCDNLists checks the list-valued cdnPolicy members.
func validateCDNLists(policy map[string]any) error {
	if headers, _ := policy["bypassCacheOnRequestHeaders"].([]any); len(headers) > maxBypassCacheHeaders {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.cdnPolicy.bypassCacheOnRequestHeaders': at most %d headers are allowed.",
			maxBypassCacheHeaders)
	}

	negPolicy, _ := policy["negativeCachingPolicy"].([]any)
	negEnabled, _ := policy["negativeCaching"].(bool)

	if len(negPolicy) > 0 && !negEnabled {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.cdnPolicy.negativeCachingPolicy': negativeCaching must be enabled.")
	}

	return nil
}

// jsonInt reads an integral JSON value: a number, or a decimal string (proto
// JSON encodes int64 fields such as signedUrlCacheMaxAgeSec as strings).
func jsonInt(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) || t < math.MinInt64 || t > math.MaxInt64 {
			return 0, false
		}

		return int64(t), true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)

		return n, err == nil
	default:
		return 0, false
	}
}
