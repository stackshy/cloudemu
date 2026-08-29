package driver

import (
	"encoding/json"
	"time"
)

const (
	// kinesisEventSource is the eventSource value Lambda stamps on every record
	// delivered from a Kinesis event-source-mapping.
	kinesisEventSource = "aws:kinesis"
	// kinesisEventName is the eventName Lambda reports for a Kinesis record.
	kinesisEventName = "aws:kinesis:record"
	// kinesisSchemaVersion / kinesisEventVersion are the fixed schema/event
	// versions Lambda reports for a Kinesis record.
	kinesisSchemaVersion = "1.0"
	kinesisEventVersion  = "1.0"
	// millisPerSecond converts a millisecond timestamp to the fractional-seconds
	// approximateArrivalTimestamp Lambda reports for a Kinesis record.
	millisPerSecond = 1000
)

// LambdaEventRecord is one Kinesis record together with the shard it landed in,
// carrying exactly the fields BuildLambdaKinesisEvent renders into a Lambda
// event record. Data is the raw record bytes; the builder base64-encodes them.
type LambdaEventRecord struct {
	ShardID        string
	SequenceNumber string
	PartitionKey   string
	Data           []byte
	ArrivalTime    time.Time
}

// BuildLambdaKinesisEvent renders a batch of Kinesis records into the exact JSON
// event Lambda delivers to a Kinesis event-source-mapping target:
// {"Records":[{kinesis:{kinesisSchemaVersion,partitionKey,sequenceNumber,data,
// approximateArrivalTimestamp},eventSource:"aws:kinesis",eventVersion,eventID,
// eventName:"aws:kinesis:record",awsRegion,eventSourceARN}]}. data is the
// base64-encoded record payload, matching the shape aws-sdk events.KinesisEvent
// binds. See https://docs.aws.amazon.com/lambda/latest/dg/with-kinesis.html.
func BuildLambdaKinesisEvent(eventSourceARN, region string, recs []LambdaEventRecord) []byte {
	records := make([]map[string]any, 0, len(recs))
	for i := range recs {
		records = append(records, lambdaKinesisRecord(&recs[i], eventSourceARN, region))
	}

	b, err := json.Marshal(map[string]any{"Records": records})
	if err != nil {
		return []byte(`{"Records":[]}`)
	}

	return b
}

func lambdaKinesisRecord(rec *LambdaEventRecord, eventSourceARN, region string) map[string]any {
	return map[string]any{
		"kinesis": map[string]any{
			"kinesisSchemaVersion": kinesisSchemaVersion,
			"partitionKey":         rec.PartitionKey,
			"sequenceNumber":       rec.SequenceNumber,
			// json.Marshal base64-encodes a []byte, exactly as Lambda delivers the
			// data field.
			"data":                        rec.Data,
			"approximateArrivalTimestamp": float64(rec.ArrivalTime.UnixMilli()) / millisPerSecond,
		},
		"eventSource":    kinesisEventSource,
		"eventVersion":   kinesisEventVersion,
		"eventID":        rec.ShardID + ":" + rec.SequenceNumber,
		"eventName":      kinesisEventName,
		"awsRegion":      region,
		"eventSourceARN": eventSourceARN,
	}
}
