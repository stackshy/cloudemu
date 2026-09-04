package dynamodb

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// DynamoDB Streams event envelope constants for the shape Lambda delivers to a
// stream event-source-mapping target.
const (
	streamEventSource  = "aws:dynamodb"
	streamEventVersion = "1.1"
)

// buildLambdaStreamEvent renders a batch of change records into the exact JSON
// event Lambda delivers to a DynamoDB stream event-source-mapping target:
// {"Records":[{eventID,eventName,eventVersion,eventSource,awsRegion,
// eventSourceARN,dynamodb:{ApproximateCreationDateTime,Keys,NewImage,OldImage,
// SequenceNumber,SizeBytes,StreamViewType}}]}. Keys/NewImage/OldImage are
// AttributeValue-encoded (driver.MarshalItem), matching the shape aws-sdk
// events.DynamoDBEvent binds.
func buildLambdaStreamEvent(streamARN, region, viewType string, recs []driver.StreamRecord) []byte {
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

func lambdaStreamRecord(rec *driver.StreamRecord, streamARN, region, viewType string) map[string]any {
	dyn := map[string]any{
		"ApproximateCreationDateTime": float64(rec.Timestamp.Unix()),
		"Keys":                        driver.MarshalItem(rec.Keys),
		"SequenceNumber":              rec.SequenceNumber,
		"SizeBytes":                   streamRecordSize(rec),
		"StreamViewType":              viewType,
	}

	if rec.NewImage != nil {
		dyn["NewImage"] = driver.MarshalItem(rec.NewImage)
	}

	if rec.OldImage != nil {
		dyn["OldImage"] = driver.MarshalItem(rec.OldImage)
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
func streamRecordSize(rec *driver.StreamRecord) int {
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
