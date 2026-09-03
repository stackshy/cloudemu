package lambda

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// asyncSQSDeliverer enqueues a failed/finished asynchronous invocation's event
// into an SQS queue by ARN. The AWS SQS mock satisfies it via DeliverExternal,
// enabling real DeadLetterConfig / OnFailure-destination delivery to SQS.
type asyncSQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
}

// asyncSNSPublisher publishes a failed/finished asynchronous invocation's event
// to an SNS topic by ARN. The AWS SNS mock satisfies it via PublishExternal.
type asyncSNSPublisher interface {
	PublishExternal(ctx context.Context, topicARN, message string) error
}

// SetAsyncDestinationTargets wires the SQS and SNS backends a failed asynchronous
// invoke routes its event to: the function's DeadLetterConfig queue/topic and its
// DestinationConfig OnFailure/OnSuccess targets. Either may be nil — routing to
// that transport is then skipped, so a library user without SQS/SNS wired is
// unaffected. Called once at provider wiring time, before any invoke.
func (m *Mock) SetAsyncDestinationTargets(sqs asyncSQSDeliverer, sns asyncSNSPublisher) {
	m.dlqSQS = sqs
	m.dlqSNS = sns
}

// defaultMaxRetryAttempts is the AWS default number of asynchronous-invoke
// retries (0-2) before the event is routed to the DLQ / OnFailure destination.
const defaultMaxRetryAttempts = 2

// routeAsyncDestinations routes a finished asynchronous (Event) invocation to its
// configured destinations. A failure (out.Error set — a handler or engine error)
// goes to the DeadLetterConfig queue/topic and the OnFailure destination once
// retries are exhausted; a success may go to the OnSuccess destination. It is
// called only for InvokeType=Event, so synchronous invokes are never affected.
//
// Delivery re-enters Lambda only through SQS/SNS -> InvokeExternal, whose
// recursion guard bounds a DLQ-that-triggers-its-own-function loop; this method
// adds no invoke of its own, so it needs no guard.
//
// Retries are not actually re-run: the stub/handler/engine outcome is
// deterministic in the emulator, so re-invoking would only duplicate side
// effects and metrics. The failure is treated as retries-exhausted immediately,
// which matches the observable outcome (the event lands in the DLQ/destination).
func (m *Mock) routeAsyncDestinations(
	ctx context.Context, fd *funcData, input driver.InvokeInput, executedVersion string, out *driver.InvokeOutput,
) {
	cfg := lookupEventInvokeConfig(fd, input.Qualifier)

	if out.Error != "" {
		// DeadLetterConfig: the DLQ message body is the original event, matching
		// real Lambda (which carries the error detail in SQS/SNS message
		// attributes, a channel the cross-service seam does not expose).
		if dl := fd.awsConfig.DeadLetterConfig; dl != nil && dl.TargetArn != "" {
			m.deliverToTarget(ctx, dl.TargetArn, string(eventBody(input.Payload)))
		}

		if dest := failureDestination(cfg.DestinationConfig); dest != "" {
			m.deliverToTarget(ctx, dest, m.destinationEnvelope(fd, input, executedVersion, out, false))
		}

		return
	}

	if dest := successDestination(cfg.DestinationConfig); dest != "" {
		m.deliverToTarget(ctx, dest, m.destinationEnvelope(fd, input, executedVersion, out, true))
	}
}

// deliverToTarget routes a delivery to the transport named by the target ARN's
// service field: an SQS queue or an SNS topic. Lambda/EventBridge destination
// ARNs are accepted by the config API but not delivered here (out of scope for
// this pass); an unknown/unwired transport is a best-effort no-op, mirroring the
// asynchronous, decoupled behavior of real destinations (a bad target never
// fails the invocation).
func (m *Mock) deliverToTarget(ctx context.Context, targetARN, body string) {
	switch arnService(targetARN) {
	case "sqs":
		if m.dlqSQS != nil {
			_ = m.dlqSQS.DeliverExternal(ctx, targetARN, body)
		}
	case "sns":
		if m.dlqSNS != nil {
			_ = m.dlqSNS.PublishExternal(ctx, targetARN, body)
		}
	}
}

// failureDestination returns the OnFailure destination ARN from a
// DestinationConfig, or "" when none is configured.
func failureDestination(dc *driver.DestinationConfig) string {
	if dc != nil && dc.OnFailure != nil {
		return dc.OnFailure.Destination
	}

	return ""
}

// successDestination returns the OnSuccess destination ARN, or "" when none.
func successDestination(dc *driver.DestinationConfig) string {
	if dc != nil && dc.OnSuccess != nil {
		return dc.OnSuccess.Destination
	}

	return ""
}

// arnService extracts the service field (index 2) from an ARN
// (arn:aws:<service>:region:account:resource); "" for a non-ARN string.
func arnService(arn string) string {
	parts := strings.SplitN(arn, ":", 6) //nolint:mnd // ARN has 6 colon-separated fields.
	if len(parts) < 3 || parts[0] != "arn" {
		return ""
	}

	return parts[2]
}

// eventBody returns the invocation event payload, substituting an empty JSON
// object for an empty payload so the DLQ message is always valid JSON.
func eventBody(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("{}")
	}

	return payload
}

// destinationEnvelope builds the AWS Lambda async-destination event that wraps a
// finished invocation's request/response with its outcome (the shape delivered
// to an OnSuccess/OnFailure destination). success selects the Success vs
// RetriesExhausted condition and the response context.
func (m *Mock) destinationEnvelope(
	fd *funcData, input driver.InvokeInput, executedVersion string, out *driver.InvokeOutput, success bool,
) string {
	condition := "RetriesExhausted"
	if success {
		condition = "Success"
	}

	respCtx := map[string]any{
		"statusCode":      out.StatusCode,
		"executedVersion": executedVersion,
	}
	if !success {
		respCtx["functionError"] = "Unhandled"
	}

	envelope := map[string]any{
		"version":   "1.0",
		"timestamp": m.opts.Clock.Now().UTC().Format(timeFormat),
		"requestContext": map[string]any{
			"requestId":              newRequestID(),
			"functionArn":            qualifiedARN(fd.info.ARN, executedVersion),
			"condition":              condition,
			"approximateInvokeCount": approximateInvokeCount(fd, input.Qualifier),
		},
		"requestPayload":  json.RawMessage(eventBody(input.Payload)),
		"responseContext": respCtx,
		"responsePayload": json.RawMessage(responsePayload(out, success)),
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return string(eventBody(input.Payload))
	}

	return string(body)
}

// responsePayload renders the destination envelope's responsePayload: the
// handler result on success (or {} when empty), or the Lambda error object on
// failure.
func responsePayload(out *driver.InvokeOutput, success bool) []byte {
	if success {
		return eventBody(out.Payload)
	}

	body, err := json.Marshal(map[string]string{
		"errorType":    "HandlerError",
		"errorMessage": out.Error,
	})
	if err != nil {
		return []byte(`{"errorMessage":"unknown error"}`)
	}

	return body
}

// approximateInvokeCount is the total number of attempts Lambda reports it made
// before routing to the destination: the configured retry attempts plus the
// initial invocation.
func approximateInvokeCount(fd *funcData, qualifier string) int {
	cfg := lookupEventInvokeConfig(fd, qualifier)

	retries := defaultMaxRetryAttempts
	if cfg.MaximumRetryAttempts != nil {
		retries = *cfg.MaximumRetryAttempts
	}

	return retries + 1
}

// qualifiedARN appends a version/alias qualifier to a function ARN, mirroring the
// functionArn real Lambda reports in the destination envelope.
func qualifiedARN(arn, qualifier string) string {
	if qualifier == "" {
		return arn
	}

	return arn + ":" + qualifier
}

// newRequestID generates a random hex request id for the destination envelope's
// requestContext (a stand-in for the invocation's AWS request id).
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}

	return hex.EncodeToString(b[:])
}
