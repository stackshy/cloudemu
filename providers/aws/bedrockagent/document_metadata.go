package bedrockagent

import (
	"context"
	"encoding/json"
	"strings"
)

// DocumentMetadata union members and attribute value types from the service
// model.
const (
	metadataInline     = "IN_LINE_ATTRIBUTE"
	metadataS3Location = "S3_LOCATION"
	attrString         = "STRING"
	attrNumber         = "NUMBER"
	attrBoolean        = "BOOLEAN"
	attrStringList     = "STRING_LIST"
)

// documentMetadata is a document's DocumentMetadata.
type documentMetadata struct {
	Type             string            `json:"type"`
	InlineAttributes []inlineAttribute `json:"inlineAttributes,omitempty"`
	S3Location       *struct {
		URI string `json:"uri"`
	} `json:"s3Location,omitempty"`
}

type inlineAttribute struct {
	Key   string         `json:"key"`
	Value attributeValue `json:"value"`
}

type attributeValue struct {
	Type            string   `json:"type"`
	StringValue     *string  `json:"stringValue,omitempty"`
	NumberValue     *float64 `json:"numberValue,omitempty"`
	BooleanValue    *bool    `json:"booleanValue,omitempty"`
	StringListValue []string `json:"stringListValue,omitempty"`
}

// value returns the attribute as a string, float64, bool or []string.
func (a *attributeValue) value() (any, bool) {
	switch {
	case a.Type == attrString && a.StringValue != nil:
		return *a.StringValue, true
	case a.Type == attrNumber && a.NumberValue != nil:
		return *a.NumberValue, true
	case a.Type == attrBoolean && a.BooleanValue != nil:
		return *a.BooleanValue, true
	case a.Type == attrStringList:
		return append([]string{}, a.StringListValue...), true
	default:
		return nil, false
	}
}

// resolveS3Metadata replaces S3_LOCATION metadata (a metadata file in S3) with
// the inline attributes it holds, so a stored document carries its attributes.
func (m *Mock) resolveS3Metadata(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var md documentMetadata
	if m.objects == nil || json.Unmarshal(raw, &md) != nil || md.Type != metadataS3Location || md.S3Location == nil {
		return raw
	}

	bucket, key, ok := strings.Cut(strings.TrimPrefix(md.S3Location.URI, s3URIPrefix), "/")
	if !ok {
		return raw
	}

	if inline := m.readSidecar(ctx, bucket, key); inline != nil {
		return inline
	}

	return raw
}

// documentAttributes returns a document's inline metadata attributes by key.
func documentAttributes(raw json.RawMessage) map[string]any {
	var md documentMetadata
	if len(raw) == 0 || json.Unmarshal(raw, &md) != nil || md.Type != metadataInline {
		return nil
	}

	out := make(map[string]any, len(md.InlineAttributes))

	for i := range md.InlineAttributes {
		if v, ok := md.InlineAttributes[i].Value.value(); ok {
			out[md.InlineAttributes[i].Key] = v
		}
	}

	return out
}
