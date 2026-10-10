package persist

import (
	"encoding/json"
	"testing"
)

func TestLegacyIDFloor(t *testing.T) {
	cases := []struct {
		name string
		data string
		want uint64
	}{
		{"prefixed id", `{"id":"i-0000002a"}`, 0x2a},
		{"prefix ending in a hex letter", `{"id":"ANPA0000003b"}`, 0x3b},
		{"upper-case id", `{"id":"Z0000004C"}`, 0x4c},
		{"concatenated ids take the newest", `{"id":"0000000100000009"}`, 9},
		{"ocid suffix", `{"id":"ocid1.vcn.oc1.iad.aaaaaaaa0000000000000011"}`, 0x11},
		{"id inside an arn", `{"arn":"arn:aws:acm:us-east-1:123456789012:certificate/00000020"}`, 0x20},
		{"id inside a hostname", `{"e":"vpc-d-00000030.us-east-1.es.amazonaws.com"}`, 0x30},
		{"map key", `{"i-00000040":{}}`, 0x40},
		{"random hex above the cap is ignored", `{"id":"i-0000002a","u":"9f3c2b1e"}`, 0x2a},
		{"short runs are ignored", `{"id":"abc123"}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacyIDFloor([]byte(tc.data)); got != tc.want {
				t.Fatalf("legacyIDFloor(%s) = %#x, want %#x", tc.data, got, tc.want)
			}
		})
	}
}

func TestIDFloorPrefersRecordedCounter(t *testing.T) {
	ps := &ProviderState{
		Services:  map[string]json.RawMessage{"ec2": json.RawMessage(`{"id":"i-000000ff"}`)},
		IDCounter: 7,
	}

	if got := idFloor(ps); got != 7 {
		t.Fatalf("idFloor = %d, want the recorded 7", got)
	}

	ps.IDCounter = 0
	if got := idFloor(ps); got != 0xff {
		t.Fatalf("legacy idFloor = %#x, want 0xff", got)
	}

	if got := idFloor(&ProviderState{}); got != 0 {
		t.Fatalf("empty idFloor = %d, want 0", got)
	}
}
