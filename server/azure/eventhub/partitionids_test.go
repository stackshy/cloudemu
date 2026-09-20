package eventhub

import "testing"

// TestPartitionIDs covers the partitionCount → shard-id-slice allocation
// (go/uncontrolled-allocation-size): a legitimate count round-trips, and a
// pathologically large or negative count — which a Premium/Dedicated-tier
// request can carry unchecked past the Basic/Standard maxPartitionCount
// validation — is clamped rather than driving an unbounded allocation.
func TestPartitionIDs(t *testing.T) {
	cases := []struct {
		name  string
		count int64
		want  int
	}{
		{"zero", 0, 0},
		{"typical", 4, 4},
		{"negative", -1, 0},
		{"atCeiling", maxAllocablePartitions, maxAllocablePartitions},
		{"aboveCeiling", maxAllocablePartitions + 1, maxAllocablePartitions},
		{"pathologicallyLarge", 1 << 40, maxAllocablePartitions},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := partitionIDs(tc.count)
			if len(got) != tc.want {
				t.Fatalf("partitionIDs(%d) returned %d ids, want %d", tc.count, len(got), tc.want)
			}

			if tc.want > 0 && got[0] != "0" {
				t.Fatalf("partitionIDs(%d)[0] = %q, want \"0\"", tc.count, got[0])
			}
		})
	}
}
