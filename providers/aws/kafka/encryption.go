package kafka

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// encryptionInfoKey is the RawOptions key holding a provisioned cluster's
// EncryptionInfo block (the wire layer stores the request block under it).
const encryptionInfoKey = "encryptionInfo"

// managedKafkaKeyARN returns the ARN of this account+region's AWS managed key
// for MSK (alias/aws/kafka). Real MSK encrypts data at rest under that key when
// a create names none, and DescribeCluster reports its key ARN in
// encryptionAtRest.dataVolumeKMSKeyId
// (https://docs.aws.amazon.com/msk/latest/developerguide/msk-working-with-encryption.html).
// There is one such key per account and region, so the key id is a UUID derived
// deterministically from both: every cluster in the account+region reports the
// same key, as in AWS.
func (m *Mock) managedKafkaKeyARN() string {
	sum := sha256.Sum256([]byte("alias/aws/kafka|" + m.opts.AccountID + "|" + m.opts.Region))
	// Shape the digest as a version-4 UUID, like a real KMS key id.
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	h := hex.EncodeToString(sum[:16])
	keyID := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]

	return idgen.AWSARN("kms", m.opts.Region, m.opts.AccountID, "key/"+keyID)
}

// withDefaultEncryption returns raw with the cluster's EncryptionInfo completed
// the way real MSK completes it. Every field the request leaves out gets its
// documented default: encryptionAtRest.dataVolumeKMSKeyId the AWS managed key,
// encryptionInTransit.clientBroker TLS and encryptionInTransit.inCluster true
// (https://docs.aws.amazon.com/msk/1.0/apireference/clusters.html#clusters-model-encryptionintransit).
// Fields the request sets, including inCluster false, are kept, as is any other
// content of the block. A block that is not a JSON object is left as sent. raw
// is not mutated.
func (m *Mock) withDefaultEncryption(raw map[string]json.RawMessage) map[string]json.RawMessage {
	out := copyRaw(raw)
	if out == nil {
		out = map[string]json.RawMessage{}
	}

	var info map[string]json.RawMessage
	if blk := out[encryptionInfoKey]; len(blk) > 0 {
		if err := json.Unmarshal(blk, &info); err != nil {
			return out
		}
	}

	if info == nil {
		info = map[string]json.RawMessage{}
	}

	changed := defaultField(info, "encryptionAtRest", "dataVolumeKMSKeyId", m.managedKafkaKeyARN())
	changed = defaultField(info, "encryptionInTransit", "clientBroker", "TLS") || changed
	changed = defaultField(info, "encryptionInTransit", "inCluster", true) || changed

	if changed {
		out[encryptionInfoKey] = mustJSON(info)
	}

	return out
}

// defaultField sets info[block][field] to val when the field is absent, null or
// an empty string, creating the block if needed, and reports whether it did.
func defaultField(info map[string]json.RawMessage, block, field string, val any) bool {
	var blk map[string]json.RawMessage
	if r, ok := info[block]; ok {
		_ = json.Unmarshal(r, &blk)
	}

	if blk == nil {
		blk = map[string]json.RawMessage{}
	}

	if v, ok := blk[field]; ok && string(v) != "null" && string(v) != `""` {
		return false
	}

	blk[field] = mustJSON(val)
	info[block] = mustJSON(blk)

	return true
}

// mustJSON marshals a value built from JSON-safe types (maps, strings, bools,
// raw messages), which cannot fail.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}

	return b
}
