package persist

import (
	"regexp"
	"strconv"
)

// legacyIDCap bounds the values the legacy scan accepts. A process never mints
// anywhere near 2^28 counter ids, so a larger value is random hex (a UUID or
// hash segment), not a counter id. Keeping the counter below 2^32 also keeps
// GenerateID's suffix at the 8 hex characters SDKs expect.
const legacyIDCap = 1 << 28

// counterHexLen is the width of GenerateID's hex suffix, and of the low 32 bits
// of an OCID suffix.
const counterHexLen = 8

// hexRun matches a run of hex digits long enough to hold a counter id.
var hexRun = regexp.MustCompile(`[0-9a-fA-F]{8,}`)

// idFloor returns the value the shared id counter must reach before ps is
// restored: the recorded counter, or for a snapshot written before it was
// recorded, a best-effort estimate from the ids in the state.
func idFloor(ps *ProviderState) uint64 {
	if ps.IDCounter != 0 || len(ps.Services) == 0 {
		return ps.IDCounter
	}

	var floor uint64
	for _, raw := range ps.Services {
		floor = max(floor, legacyIDFloor(raw))
	}

	return floor
}

// legacyIDFloor estimates the highest counter value used by the ids in data.
// It is best-effort: it takes the last 8 hex characters of every hex run of 8
// or more (a GenerateID suffix, the tail of an OCID, the newest of several
// concatenated ids) and ignores anything at or above legacyIDCap. Random hex
// below the cap can only push the counter higher than needed, which is safe:
// ids minted afterwards look larger but keep their 8 hex characters. Ids held
// in base64-encoded []byte fields are not decoded, so they are not seen.
func legacyIDFloor(data []byte) uint64 {
	var floor uint64

	for _, run := range hexRun.FindAll(data, -1) {
		v, err := strconv.ParseUint(string(run[len(run)-counterHexLen:]), 16, 64)
		if err != nil || v >= legacyIDCap {
			continue
		}

		floor = max(floor, v)
	}

	return floor
}
