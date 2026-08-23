package sqs

import (
	"crypto/md5" //nolint:gosec // SQS MD5 checksums are part of the wire protocol, not security.
	"encoding/binary"
	"encoding/hex"
	"sort"

	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	attrAll  = "All"
	attrTrue = "true"
)

// wireMessageAttribute is the JSON shape of a single SQS MessageAttribute.
type wireMessageAttribute struct {
	DataType    string `json:"DataType"`
	StringValue string `json:"StringValue,omitempty"`
	BinaryValue []byte `json:"BinaryValue,omitempty"`
}

// toDriverMessageAttributes converts wire message attributes into driver form.
func toDriverMessageAttributes(in map[string]wireMessageAttribute) map[string]mqdriver.MessageAttributeValue {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]mqdriver.MessageAttributeValue, len(in))

	for k, v := range in {
		out[k] = mqdriver.MessageAttributeValue{
			DataType:    v.DataType,
			StringValue: v.StringValue,
			BinaryValue: v.BinaryValue,
		}
	}

	return out
}

// fromDriverMessageAttributes converts driver message attributes into wire form,
// filtering to the requested names (empty or "All"/".*" means all).
func fromDriverMessageAttributes(
	in map[string]mqdriver.MessageAttributeValue, names []string,
) map[string]wireMessageAttribute {
	if len(in) == 0 {
		return nil
	}

	want := attributeSelector(names)

	out := make(map[string]wireMessageAttribute, len(in))

	for k, v := range in {
		if !want(k) {
			continue
		}

		out[k] = wireMessageAttribute{
			DataType:    v.DataType,
			StringValue: v.StringValue,
			BinaryValue: v.BinaryValue,
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// attributeSelector returns a predicate matching requested attribute names.
func attributeSelector(names []string) func(string) bool {
	if len(names) == 0 {
		return func(string) bool { return false }
	}

	set := make(map[string]bool, len(names))

	for _, n := range names {
		if n == attrAll || n == ".*" {
			return func(string) bool { return true }
		}

		set[n] = true
	}

	return func(k string) bool { return set[k] }
}

// md5OfBody returns the hex MD5 of a message body.
func md5OfBody(body string) string {
	sum := md5.Sum([]byte(body)) //nolint:gosec // wire checksum
	return hex.EncodeToString(sum[:])
}

// md5OfMessageAttributes computes the SQS MD5OfMessageAttributes digest over the
// typed attributes, following the AWS-documented canonical encoding. Returns ""
// when there are no attributes.
func md5OfMessageAttributes(attrs map[string]mqdriver.MessageAttributeValue) string {
	if len(attrs) == 0 {
		return ""
	}

	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}

	sort.Strings(names)

	h := md5.New() //nolint:gosec // wire checksum

	for _, name := range names {
		v := attrs[name]

		writeLenPrefixed(h, []byte(name))
		writeLenPrefixed(h, []byte(v.DataType))

		if len(v.BinaryValue) > 0 && v.StringValue == "" {
			_, _ = h.Write([]byte{2})
			writeLenPrefixed(h, v.BinaryValue)
		} else {
			_, _ = h.Write([]byte{1})
			writeLenPrefixed(h, []byte(v.StringValue))
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

// writeLenPrefixed writes a 4-byte big-endian length followed by the bytes.
func writeLenPrefixed(h interface{ Write([]byte) (int, error) }, b []byte) {
	var prefix [4]byte

	binary.BigEndian.PutUint32(prefix[:], uint32(len(b))) //nolint:gosec // attribute lengths are small and non-negative
	_, _ = h.Write(prefix[:])
	_, _ = h.Write(b)
}
