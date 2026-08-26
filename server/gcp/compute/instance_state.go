package compute

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"net"
	"sort"
	"strconv"
	"strings"
)

// GCP-specific instance state that the portable compute driver model does not
// carry (disks, network tags, metadata, the network self-link, and the launch
// zone) is round-tripped through the driver by encoding it into internal
// entries of the instance's tag map, keyed with the "cloudemu:gcp" prefix.
// These keys are stripped from the emitted labels.
const (
	keyDisks         = "cloudemu:gcp:disks"
	keyNetTags       = "cloudemu:gcp:nettags"
	keyMetadata      = "cloudemu:gcp:metadata"
	keyNetwork       = "cloudemu:gcp:network"
	keyZone          = "cloudemu:gcp:zone"
	keyAccessConfigs = "cloudemu:gcp:accessconfigs"
)

// internalTagPrefix marks tag keys that carry CloudEmu-internal GCP state
// rather than user labels. gcpNameTag ("cloudemu:gcpName") also matches.
const internalTagPrefix = "cloudemu:gcp"

// kvStride is the number of slice slots a flattened key/value pair occupies.
const kvStride = 2

// internalTagCap is the number of internal keys insertTags may add, used only
// as a map preallocation hint.
const internalTagCap = 7

func isInternalTag(key string) bool {
	return strings.HasPrefix(key, internalTagPrefix)
}

func encodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}

func decodeDisks(tags map[string]string) []attachedDisk {
	raw := tags[keyDisks]
	if raw == "" {
		return nil
	}

	var disks []attachedDisk
	if err := json.Unmarshal([]byte(raw), &disks); err != nil {
		return nil
	}

	return disks
}

func decodeAccessConfigs(tags map[string]string) []accessConfig {
	raw := tags[keyAccessConfigs]
	if raw == "" {
		return nil
	}

	var acs []accessConfig
	if err := json.Unmarshal([]byte(raw), &acs); err != nil {
		return nil
	}

	return acs
}

// ephemeralIPPrefix is the leading octet of GCP's public 34.0.0.0/8 range,
// which CloudEmu maps synthesized ephemeral external IPs into.
const ephemeralIPPrefix = 34

// ephemeralExternalIP synthesizes a deterministic external IP in GCP's public
// 34.x range for an accessConfig the caller left without a natIP (real GCP
// auto-assigns an ephemeral one). Deterministic in the instance name + index so
// repeated GETs report a stable address.
func ephemeralExternalIP(seed string, index int) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	_, _ = h.Write([]byte(strconv.Itoa(index)))

	var buf [net.IPv4len]byte

	binary.BigEndian.PutUint32(buf[:], h.Sum32())
	buf[0] = ephemeralIPPrefix

	return net.IP(buf[:]).String()
}

func decodeNetTags(tags map[string]string) []string {
	raw := tags[keyNetTags]
	if raw == "" {
		return nil
	}

	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}

	return items
}

func decodeMetadata(tags map[string]string) []metadataItem {
	raw := tags[keyMetadata]
	if raw == "" {
		return nil
	}

	var items []metadataItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}

	return items
}

// fingerprint returns a stable GCP-style fingerprint (8 base64 bytes) derived
// from parts. It changes when the underlying content changes, which is what
// clients compare for optimistic concurrency on setLabels/setMetadata/setTags.
func fingerprint(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}

	var buf [8]byte

	binary.BigEndian.PutUint64(buf[:], h.Sum64())

	return base64.StdEncoding.EncodeToString(buf[:])
}

// userLabels returns the user-visible labels: the tag map with all internal
// CloudEmu keys removed. Keys are returned sorted-stable via the map itself.
func userLabels(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))

	for k, v := range tags {
		if isInternalTag(k) {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// labelFingerprintFor derives the labelFingerprint from the sorted user labels.
func labelFingerprintFor(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys)*kvStride)
	for _, k := range keys {
		parts = append(parts, k, labels[k])
	}

	return fingerprint(parts...)
}
