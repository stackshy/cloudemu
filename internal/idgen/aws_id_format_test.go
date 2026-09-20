package idgen_test

import (
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// These guard the AWS-shaped id generators: the SDKs/CLI validate id shape
// client-side, so a too-short or wrong-charset id is rejected before the request
// is sent (breaking key rotation, tag ops, GetCommandInvocation, etc.).
func TestAWSIDFormats(t *testing.T) {
	cases := []struct {
		name string
		got  string
		re   *regexp.Regexp
	}{
		{"AccessKeyID", idgen.AccessKeyID(), regexp.MustCompile(`^AKIA[A-Z2-7]{16}$`)},
		{"TempAccessKeyID", idgen.TempAccessKeyID(), regexp.MustCompile(`^ASIA[A-Z2-7]{16}$`)},
		{"AppSyncAPIID", idgen.AppSyncAPIID(), regexp.MustCompile(`^[a-z0-9]{26}$`)},
		{"GenerateLongID", idgen.GenerateLongID("svc-"), regexp.MustCompile(`^svc-[0-9a-f]{17}$`)},
		{"UUID", idgen.UUID(), regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.re.MatchString(tc.got) {
				t.Fatalf("%s = %q, does not match %s", tc.name, tc.got, tc.re)
			}
		})
	}

	// Access key ids must be >= 16 chars total — the minimum the AWS SDKs enforce
	// client-side before UpdateAccessKey/DeleteAccessKey.
	if len(idgen.AccessKeyID()) < 16 {
		t.Fatalf("AccessKeyID length %d < 16", len(idgen.AccessKeyID()))
	}

	// Uniqueness sanity across a batch (crypto/rand-backed).
	seen := map[string]bool{}
	for range 100 {
		id := idgen.AccessKeyID()
		if seen[id] {
			t.Fatalf("duplicate AccessKeyID %q", id)
		}
		seen[id] = true
	}
}
