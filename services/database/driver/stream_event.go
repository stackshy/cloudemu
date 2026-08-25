package driver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// DynamoDB Streams event envelope constants shared by the Streams data-plane
// wire handler and Lambda event-source-mapping delivery.
const (
	streamEventSource  = "aws:dynamodb"
	streamEventVersion = "1.1"
)

// MarshalAttributeValue wraps a plain Go value (as the in-memory providers store
// items) into a single DynamoDB AttributeValue wire map, e.g. "x" -> {"S":"x"}.
// It is the canonical native->AV encoder, shared by the DynamoDB wire codec and
// by Lambda stream event delivery so both render identical AttributeValue JSON.
func MarshalAttributeValue(v any) map[string]any {
	if av, ok := marshalBinaryOrSet(v); ok {
		return av
	}

	switch val := v.(type) {
	case string:
		return map[string]any{"S": val}
	case float64:
		return map[string]any{"N": strconv.FormatFloat(val, 'f', -1, 64)}
	case int:
		return map[string]any{"N": strconv.Itoa(val)}
	case int64:
		return map[string]any{"N": strconv.FormatInt(val, 10)}
	case bool:
		return map[string]any{"BOOL": val}
	case nil:
		return map[string]any{"NULL": true}
	case []any:
		return marshalList(val)
	case map[string]any:
		return map[string]any{"M": MarshalItem(val)}
	default:
		return map[string]any{"S": fmt.Sprintf("%v", val)}
	}
}

// marshalBinaryOrSet encodes the exact-decimal Number (N), the binary scalar (B)
// and the set types (SS/NS/BS), keeping MarshalAttributeValue's type switch
// within the complexity budget.
func marshalBinaryOrSet(v any) (map[string]any, bool) {
	switch val := v.(type) {
	case expr.Number:
		return map[string]any{"N": string(val)}, true
	case []byte:
		return map[string]any{"B": base64.StdEncoding.EncodeToString(val)}, true
	case expr.StringSet:
		return map[string]any{"SS": []string(val)}, true
	case expr.NumberSet:
		return map[string]any{"NS": formatFloatSet(val)}, true
	case expr.BinarySet:
		return map[string]any{"BS": formatBinarySet(val)}, true
	default:
		return nil, false
	}
}

func formatFloatSet(ns expr.NumberSet) []string {
	out := make([]string, 0, len(ns))
	for _, f := range ns {
		out = append(out, strconv.FormatFloat(f, 'f', -1, 64))
	}

	return out
}

func formatBinarySet(bs expr.BinarySet) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, base64.StdEncoding.EncodeToString(b))
	}

	return out
}

func marshalList(list []any) map[string]any {
	items := make([]any, 0, len(list))
	for _, elem := range list {
		items = append(items, MarshalAttributeValue(elem))
	}

	return map[string]any{"L": items}
}

// MarshalItem converts a plain item map to its DynamoDB AttributeValue wire form,
// AV-encoding every attribute value. A nil item stays nil.
func MarshalItem(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}

	out := make(map[string]any, len(item))
	for k, v := range item {
		out[k] = MarshalAttributeValue(v)
	}

	return out
}

// BuildLambdaStreamEvent renders a batch of change records into the exact JSON
// event Lambda delivers to a DynamoDB stream event-source-mapping target:
// {"Records":[{eventID,eventName,eventVersion,eventSource,awsRegion,
// eventSourceARN,dynamodb:{ApproximateCreationDateTime,Keys,NewImage,OldImage,
// SequenceNumber,SizeBytes,StreamViewType}}]}. Keys/NewImage/OldImage are
// AttributeValue-encoded, matching the shape aws-sdk events.DynamoDBEvent binds.
func BuildLambdaStreamEvent(streamARN, region, viewType string, recs []StreamRecord) []byte {
	records := make([]map[string]any, 0, len(recs))
	for i := range recs {
		records = append(records, lambdaStreamRecord(&recs[i], streamARN, region, viewType))
	}

	b, err := json.Marshal(map[string]any{"Records": records})
	if err != nil {
		return []byte(`{"Records":[]}`)
	}

	return b
}

func lambdaStreamRecord(rec *StreamRecord, streamARN, region, viewType string) map[string]any {
	dyn := map[string]any{
		"ApproximateCreationDateTime": float64(rec.Timestamp.Unix()),
		"Keys":                        MarshalItem(rec.Keys),
		"SequenceNumber":              rec.SequenceNumber,
		"SizeBytes":                   streamRecordSize(rec),
		"StreamViewType":              viewType,
	}

	if rec.NewImage != nil {
		dyn["NewImage"] = MarshalItem(rec.NewImage)
	}

	if rec.OldImage != nil {
		dyn["OldImage"] = MarshalItem(rec.OldImage)
	}

	return map[string]any{
		"eventID":        rec.EventID,
		"eventName":      rec.EventType,
		"eventVersion":   streamEventVersion,
		"eventSource":    streamEventSource,
		"awsRegion":      region,
		"eventSourceARN": streamARN,
		"dynamodb":       dyn,
	}
}

// streamRecordSize estimates a record's on-the-wire size, as DynamoDB reports it
// on each stream record: a JSON byte count of the captured keys/images.
func streamRecordSize(rec *StreamRecord) int {
	size := 0

	for _, m := range []map[string]any{rec.Keys, rec.NewImage, rec.OldImage} {
		if m == nil {
			continue
		}

		if b, err := json.Marshal(m); err == nil {
			size += len(b)
		}
	}

	return size
}
