package kms

import (
	"encoding/json"
	"testing"
)

func TestRawEnumNormalizeAcceptsStringAndInt(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		names   map[int32]string
		want    string
		wantOK  bool
		present bool
	}{
		{"purpose string", `"ENCRYPT_DECRYPT"`, purposeNames, "ENCRYPT_DECRYPT", true, true},
		{"purpose int 1", `1`, purposeNames, "ENCRYPT_DECRYPT", true, true},
		{"purpose int 5 asym-sign", `5`, purposeNames, "ASYMMETRIC_SIGN", true, true},
		{"purpose lowercase string", `"encrypt_decrypt"`, purposeNames, "ENCRYPT_DECRYPT", true, true},
		{"state int 1 enabled", `1`, stateNames, "ENABLED", true, true},
		{"state int 5 pending-gen", `5`, stateNames, "PENDING_GENERATION", true, true},
		{"protection int 2 hsm", `2`, protectionLevelNames, "HSM", true, true},
		{"algorithm int 1", `1`, algorithmNames, "GOOGLE_SYMMETRIC_ENCRYPTION", true, true},
		{"unknown int", `999`, purposeNames, "", false, true},
		{"unknown string", `"NOPE"`, purposeNames, "", false, true},
		{"absent", `null`, purposeNames, "", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e rawEnum
			if err := json.Unmarshal([]byte(tc.raw), &e); err != nil {
				t.Fatalf("unmarshal %q: %v", tc.raw, err)
			}

			got, ok, present := e.normalize(tc.names)
			if got != tc.want || ok != tc.wantOK || present != tc.present {
				t.Fatalf("normalize(%q) = (%q,%v,%v), want (%q,%v,%v)",
					tc.raw, got, ok, present, tc.want, tc.wantOK, tc.present)
			}
		})
	}
}

func TestParseRouteClassifiesEndpoints(t *testing.T) {
	base := "/v1/projects/p/locations/l/keyRings"

	cases := []struct {
		path     string
		wantKind routeKind
		verb     string
		ring     string
		key      string
		version  string
	}{
		{base, kindKeyRingColl, "", "", "", ""},
		{base + "/r", kindKeyRing, "", "r", "", ""},
		{base + "/r:getIamPolicy", kindKeyRing, "getIamPolicy", "r", "", ""},
		{base + "/r/cryptoKeys", kindCryptoKeyColl, "", "r", "", ""},
		{base + "/r/cryptoKeys/k", kindCryptoKey, "", "r", "k", ""},
		{base + "/r/cryptoKeys/k:updatePrimaryVersion", kindCryptoKey, "updatePrimaryVersion", "r", "k", ""},
		{base + "/r/cryptoKeys/k/cryptoKeyVersions", kindVersionColl, "", "r", "k", ""},
		{base + "/r/cryptoKeys/k/cryptoKeyVersions/3", kindVersion, "", "r", "k", "3"},
		{base + "/r/cryptoKeys/k/cryptoKeyVersions/3:destroy", kindVersion, "destroy", "r", "k", "3"},
	}

	for _, tc := range cases {
		rt, ok := parseRoute(tc.path)
		if !ok {
			t.Fatalf("parseRoute(%q) not ok", tc.path)
		}

		if rt.kind != tc.wantKind || rt.verb != tc.verb ||
			rt.keyRing != tc.ring || rt.cryptoKey != tc.key || rt.version != tc.version {
			t.Fatalf("parseRoute(%q) = %+v", tc.path, rt)
		}
	}

	// Malformed tails are rejected.
	for _, bad := range []string{
		"/v1/projects/p/locations/l/keyRings/r/badseg",
		"/v1/projects/p/locations/l/keyRings/r/cryptoKeys/k/badseg",
		"/v1/projects/p/secrets/s",
	} {
		if _, ok := parseRoute(bad); ok {
			t.Fatalf("parseRoute(%q) = ok, want rejected", bad)
		}
	}
}
