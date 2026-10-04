// Package opmeta builds the pieces of a completed google.longrunning.Operation
// that location-scoped GCP services share: a unique operation id, the
// OperationMetadata Any, and the google.protobuf.Empty response a delete
// carries. A GAPIC client's op.Wait() decodes `response` (and Metadata() decodes
// `metadata`) as a google.protobuf.Any, so both must carry an "@type".
package opmeta

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// EmptyTypeURL is the Any type URL of a delete operation's response.
const EmptyTypeURL = "type.googleapis.com/google.protobuf.Empty"

const randBytes = 4

// NewID returns an operation id in real GCP's shape,
// "operation-<unixMilli>-<8 hex>". The random suffix keeps two operations
// minted in the same millisecond (or for the same resource) from sharing a
// name, so a later mint never overwrites an earlier operation's record.
func NewID(now time.Time) string {
	b := make([]byte, randBytes)
	if _, err := rand.Read(b); err != nil {
		return "operation-" + strconv.FormatInt(now.UnixNano(), 10)
	}

	return "operation-" + strconv.FormatInt(now.UnixMilli(), 10) + "-" + hex.EncodeToString(b)
}

// Empty returns the google.protobuf.Empty Any a completed delete carries.
func Empty() json.RawMessage {
	return json.RawMessage(`{"@type":"` + EmptyTypeURL + `"}`)
}

// metadata is the common shape of the per-service OperationMetadata messages
// (alloydb.v1, eventarc.v1 and the google.cloud.common one Filestore uses
// all declare these fields).
type metadata struct {
	CreateTime string `json:"createTime,omitempty"`
	EndTime    string `json:"endTime,omitempty"`
	Target     string `json:"target,omitempty"`
	Verb       string `json:"verb,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

// Metadata returns an OperationMetadata Any of the given type URL for an
// operation on target that started and finished at now.
func Metadata(typeURL string, now time.Time, target, verb string) json.RawMessage {
	ts := gcprest.FormatTime(now)

	return Response(metadata{CreateTime: ts, EndTime: ts, Target: target, Verb: verb, APIVersion: "v1"}, typeURL)
}

// Response returns v as an Any of the given type URL, or nil when v cannot be
// rendered as a JSON object.
func Response(v any, typeURL string) json.RawMessage {
	raw, err := gcprest.TypedAny(v, typeURL)
	if err != nil {
		return nil
	}

	return raw
}
