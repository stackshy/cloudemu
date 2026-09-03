package gcs

import "encoding/base64"

const (
	// iamEtagTag is the protobuf tag byte for field 1 (varint wire type) —
	// real GCS bucket IAM policy etags are base64 of a single-field protobuf
	// message carrying a monotonically increasing version, e.g. "CAE=" decodes
	// to tag 0x08 (field 1, varint) followed by the varint-encoded value 1.
	iamEtagTag = 0x08
	// varintContinuationBit marks that a varint byte is followed by more
	// payload bytes.
	varintContinuationBit = 0x80
	// varintPayloadMask extracts the 7 payload bits of one varint byte.
	varintPayloadMask = 0x7f
	// varintPayloadBits is how many bits of payload one varint byte carries.
	varintPayloadBits = 7
	// iamEtagInitialVersion is the version real GCS reports for a bucket
	// whose IAM policy was never explicitly set (etag "CAE=").
	iamEtagInitialVersion = 1
)

// nextIAMEtag mints the etag that follows prevEtag, incrementing its encoded
// version. A prevEtag that isn't in the expected format (e.g. one this
// emulator never minted) is treated as the initial version, so the result is
// still a well-formed, freshly incremented etag.
func nextIAMEtag(prevEtag string) string {
	version, ok := decodeIAMEtagVersion(prevEtag)
	if !ok {
		version = iamEtagInitialVersion
	}

	return encodeIAMEtag(version + 1)
}

// encodeIAMEtag base64-encodes a protobuf field-1 varint carrying version.
func encodeIAMEtag(version uint64) string {
	b := []byte{iamEtagTag}

	for version >= varintContinuationBit {
		// version&varintPayloadMask|varintContinuationBit is masked to the
		// low 8 bits (0x00-0xff), so the byte conversion never truncates.
		b = append(b, byte(version&varintPayloadMask|varintContinuationBit)) //nolint:gosec // masked to a single byte above
		version >>= varintPayloadBits
	}

	b = append(b, byte(version))

	return base64.StdEncoding.EncodeToString(b)
}

// decodeIAMEtagVersion reverses encodeIAMEtag, reporting ok=false for any
// etag not in that format (opaque values a real client should never
// construct, but a defensive fallback keeps setIamPolicy from erroring on
// one).
func decodeIAMEtagVersion(etag string) (uint64, bool) {
	b, err := base64.StdEncoding.DecodeString(etag)
	if err != nil || len(b) < 2 || b[0] != iamEtagTag {
		return 0, false
	}

	var version uint64

	var shift uint

	for _, c := range b[1:] {
		version |= uint64(c&varintPayloadMask) << shift
		if c&varintContinuationBit == 0 {
			return version, true
		}

		shift += varintPayloadBits
	}

	return 0, false
}
