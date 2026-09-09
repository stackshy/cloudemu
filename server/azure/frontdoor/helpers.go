package frontdoor

import (
	"encoding/base32"
	"encoding/binary"
	"hash/fnv"
	"strings"
)

// Small generic-JSON and computed-value helpers shared by the profile, endpoint
// and origin-group operations. Extraction helpers are defensive: a missing or
// wrongly-typed field yields the zero value rather than panicking, so a malformed
// request body never 500s.

// orDefaultLocation returns loc, or the Front Door default location ("global")
// when loc is empty.
func orDefaultLocation(loc string) string {
	if loc != "" {
		return loc
	}

	return defaultLocation
}

// stripKeys returns a copy of props with the given keys removed, or nil when the
// result is empty. The computed stamps (frontDoorId, provisioningState, ...) are
// dropped so a client-echoed stale value never doubles up on read.
func stripKeys(props map[string]any, keys ...string) map[string]any {
	if len(props) == 0 {
		return nil
	}

	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}

	out := make(map[string]any, len(props))

	for k, v := range props {
		if _, skip := drop[k]; skip {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// overlayProps returns a shallow copy of base with every key from patch applied
// on top, backing a PATCH that merges supplied property keys over the stored ones.
func overlayProps(base, patch map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range patch {
		out[k] = v
	}

	return out
}

// endpointHostName derives the computed, stable Front Door endpoint hostName
// "<name>-<token>.z01.azurefd.net". token is the first 10 chars of the lowercase
// base32 of fnv64(profileID+"/afdEndpoints/"+name), so it is deterministic and
// never drifts across GETs.
func endpointHostName(profileID, name string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(profileID + "/afdEndpoints/" + name))

	var b [8]byte

	binary.BigEndian.PutUint64(b[:], h.Sum64())

	token := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
	if len(token) > hostNameTokenLen {
		token = token[:hostNameTokenLen]
	}

	return name + "-" + token + hostNameSuffix
}
