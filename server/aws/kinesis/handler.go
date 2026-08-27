// Package kinesis implements the AWS Kinesis Data Streams JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/kinesis client (or the
// `aws kinesis` CLI) at a Server registered with this handler and stream
// operations run against an in-memory Kinesis driver.
//
// Kinesis uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "Kinesis_20131202.").
package kinesis

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

const targetPrefix = "Kinesis_20131202."

// Handler serves Kinesis JSON-RPC requests against a Kinesis driver.
type Handler struct {
	kinesis kinesisdriver.Kinesis
	routes  map[string]http.HandlerFunc
}

// New returns a Kinesis handler backed by d.
func New(d kinesisdriver.Kinesis) *Handler {
	h := &Handler{kinesis: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateStream":                  h.createStream,
		"DeleteStream":                  h.deleteStream,
		"DescribeStream":                h.describeStream,
		"DescribeStreamSummary":         h.describeStreamSummary,
		"ListStreams":                   h.listStreams,
		"IncreaseStreamRetentionPeriod": h.increaseRetention,
		"DecreaseStreamRetentionPeriod": h.decreaseRetention,
		"UpdateShardCount":              h.updateShardCount,
		"UpdateStreamMode":              h.updateStreamMode,
		"MergeShards":                   h.mergeShards,
		"SplitShard":                    h.splitShard,
		"StartStreamEncryption":         h.startStreamEncryption,
		"StopStreamEncryption":          h.stopStreamEncryption,
		"UpdateMaxRecordSize":           h.updateMaxRecordSize,
		"UpdateStreamWarmThroughput":    h.updateStreamWarmThroughput,
		"PutRecord":                     h.putRecord,
		"PutRecords":                    h.putRecords,
		"GetShardIterator":              h.getShardIterator,
		"GetRecords":                    h.getRecords,
		"ListShards":                    h.listShards,
		"RegisterStreamConsumer":        h.registerConsumer,
		"DeregisterStreamConsumer":      h.deregisterConsumer,
		"DescribeStreamConsumer":        h.describeConsumer,
		"ListStreamConsumers":           h.listConsumers,
		"SubscribeToShard":              h.subscribeToShard,
		"EnableEnhancedMonitoring":      h.enableMonitoring,
		"DisableEnhancedMonitoring":     h.disableMonitoring,
		"AddTagsToStream":               h.addTags,
		"RemoveTagsFromStream":          h.removeTags,
		"ListTagsForStream":             h.listTagsForStream,
		"TagResource":                   h.tagResource,
		"UntagResource":                 h.untagResource,
		"ListTagsForResource":           h.listTagsForResource,
		"PutResourcePolicy":             h.putResourcePolicy,
		"GetResourcePolicy":             h.getResourcePolicy,
		"DeleteResourcePolicy":          h.deleteResourcePolicy,
		"DescribeLimits":                h.describeLimits,
		"DescribeAccountSettings":       h.describeAccountSettings,
		"UpdateAccountSettings":         h.updateAccountSettings,
	}

	return h
}

// Matches returns true for Kinesis-shaped requests (X-Amz-Target of
// "Kinesis_20131202.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Kinesis operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Kinesis operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error).
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// writeErr maps a driver error to the closest Kinesis JSON error type, honoring
// a tagged driver.APIError exception when present.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *kinesisdriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceInUseException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidArgumentException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}
