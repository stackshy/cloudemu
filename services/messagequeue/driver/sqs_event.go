package driver

import (
	"crypto/md5" //nolint:gosec // SQS MD5 checksums are a wire-protocol field, not a security use.
	"encoding/hex"
	"encoding/json"
)

// sqsEventSource is the eventSource value Lambda stamps on every record
// delivered from an SQS event-source-mapping.
const sqsEventSource = "aws:sqs"

// BuildLambdaSQSEvent renders a batch of received messages into the exact JSON
// event Lambda delivers to an SQS event-source-mapping target:
// {"Records":[{messageId,receiptHandle,body,attributes,messageAttributes,
// md5OfBody,eventSource:"aws:sqs",eventSourceARN,awsRegion}]}. attributes
// carries the SQS system attributes exactly as Message.SystemAttributes
// already holds them (ApproximateReceiveCount, SentTimestamp, SenderId,
// ApproximateFirstReceiveTimestamp, and for FIFO queues SequenceNumber,
// MessageGroupId, MessageDeduplicationId).
// See https://docs.aws.amazon.com/lambda/latest/dg/with-sqs.html.
func BuildLambdaSQSEvent(eventSourceARN, region string, msgs []Message) []byte {
	records := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		records = append(records, lambdaSQSRecord(&msgs[i], eventSourceARN, region))
	}

	b, err := json.Marshal(map[string]any{"Records": records})
	if err != nil {
		return []byte(`{"Records":[]}`)
	}

	return b
}

func lambdaSQSRecord(msg *Message, eventSourceARN, region string) map[string]any {
	return map[string]any{
		"messageId":         msg.MessageID,
		"receiptHandle":     msg.ReceiptHandle,
		"body":              msg.Body,
		"attributes":        msg.SystemAttributes,
		"messageAttributes": lambdaMessageAttributes(msg.MessageAttributes),
		"md5OfBody":         md5OfBody(msg.Body),
		"eventSource":       sqsEventSource,
		"eventSourceARN":    eventSourceARN,
		"awsRegion":         region,
	}
}

// lambdaMessageAttributes renders typed SQS message attributes into the shape
// Lambda's SQS event binds them as: stringValue/binaryValue plus the always-
// present (if empty) stringListValues/binaryListValues arrays and dataType.
func lambdaMessageAttributes(attrs map[string]MessageAttributeValue) map[string]any {
	out := make(map[string]any, len(attrs))

	for k, v := range attrs {
		rec := map[string]any{
			"stringValue":      v.StringValue,
			"stringListValues": []string{},
			"binaryListValues": [][]byte{},
			"dataType":         v.DataType,
		}

		if len(v.BinaryValue) > 0 {
			rec["binaryValue"] = v.BinaryValue
		}

		out[k] = rec
	}

	return out
}

// md5OfBody returns the hex MD5 checksum of a message body, matching the
// md5OfBody field SQS/Lambda report for a message.
func md5OfBody(body string) string {
	sum := md5.Sum([]byte(body)) //nolint:gosec // SQS MD5 checksum, not a security use.
	return hex.EncodeToString(sum[:])
}
