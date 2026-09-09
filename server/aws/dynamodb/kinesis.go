package dynamodb

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// routeKinesis dispatches the Kinesis Data Streams streaming-destination
// operations. Without them Enable/Disable/Update/DescribeKinesisStreamingDestination
// return UnknownOperationException and the aws_dynamodb_kinesis_streaming_destination
// resource cannot be managed.
func (h *Handler) routeKinesis(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "EnableKinesisStreamingDestination":
		h.enableKinesisStreamingDestination(w, r)
	case "DisableKinesisStreamingDestination":
		h.disableKinesisStreamingDestination(w, r)
	case "UpdateKinesisStreamingDestination":
		h.updateKinesisStreamingDestination(w, r)
	case "DescribeKinesisStreamingDestination":
		h.describeKinesisStreamingDestination(w, r)
	default:
		return false
	}

	return true
}

// kinesisStreamer returns the driver's KinesisStreamer capability, writing an
// error response and returning false when the driver does not implement it.
func (h *Handler) kinesisStreamer(w http.ResponseWriter) (dbdriver.KinesisStreamer, bool) {
	k, ok := h.db.(dbdriver.KinesisStreamer)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "kinesis streaming destinations are not supported by this driver")

		return nil, false
	}

	return k, true
}

// kinesisRequest is the shared wire shape for the enable/disable/update
// streaming-destination operations.
type kinesisRequest struct {
	TableName                           string `json:"TableName"`
	StreamArn                           string `json:"StreamArn"`
	EnableKinesisStreamingConfiguration *struct {
		ApproximateCreationDateTimePrecision string `json:"ApproximateCreationDateTimePrecision"`
	} `json:"EnableKinesisStreamingConfiguration"`
	UpdateKinesisStreamingConfiguration *struct {
		ApproximateCreationDateTimePrecision string `json:"ApproximateCreationDateTimePrecision"`
	} `json:"UpdateKinesisStreamingConfiguration"`
}

// kinesisAction performs one destination mutation identified by act, given the
// decoded request; the enable/disable/update handlers differ only in act.
type kinesisAction func(k dbdriver.KinesisStreamer, r *http.Request, req *kinesisRequest) (dbdriver.KinesisDestination, error)

// mutateKinesisDestination is the shared body of the enable/disable/update
// operations: resolve the capability, decode the request, apply act and render
// the resulting destination.
func (h *Handler) mutateKinesisDestination(w http.ResponseWriter, r *http.Request, act kinesisAction) {
	k, ok := h.kinesisStreamer(w)
	if !ok {
		return
	}

	var req kinesisRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	dest, err := act(k, r, &req)
	if err != nil {
		writeKinesisErr(w, err)
		return
	}

	writeKinesisDestination(w, req.TableName, dest)
}

func (h *Handler) enableKinesisStreamingDestination(w http.ResponseWriter, r *http.Request) {
	h.mutateKinesisDestination(w, r, func(k dbdriver.KinesisStreamer,
		r *http.Request, req *kinesisRequest) (dbdriver.KinesisDestination, error) {
		precision := ""
		if req.EnableKinesisStreamingConfiguration != nil {
			precision = req.EnableKinesisStreamingConfiguration.ApproximateCreationDateTimePrecision
		}

		return k.EnableKinesisStreamingDestination(r.Context(), req.TableName, req.StreamArn, precision)
	})
}

func (h *Handler) disableKinesisStreamingDestination(w http.ResponseWriter, r *http.Request) {
	h.mutateKinesisDestination(w, r, func(k dbdriver.KinesisStreamer,
		r *http.Request, req *kinesisRequest) (dbdriver.KinesisDestination, error) {
		return k.DisableKinesisStreamingDestination(r.Context(), req.TableName, req.StreamArn)
	})
}

func (h *Handler) updateKinesisStreamingDestination(w http.ResponseWriter, r *http.Request) {
	h.mutateKinesisDestination(w, r, func(k dbdriver.KinesisStreamer,
		r *http.Request, req *kinesisRequest) (dbdriver.KinesisDestination, error) {
		precision := ""
		if req.UpdateKinesisStreamingConfiguration != nil {
			precision = req.UpdateKinesisStreamingConfiguration.ApproximateCreationDateTimePrecision
		}

		return k.UpdateKinesisStreamingDestination(r.Context(), req.TableName, req.StreamArn, precision)
	})
}

func (h *Handler) describeKinesisStreamingDestination(w http.ResponseWriter, r *http.Request) {
	k, ok := h.kinesisStreamer(w)
	if !ok {
		return
	}

	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	dests, err := k.DescribeKinesisStreamingDestination(r.Context(), req.TableName)
	if err != nil {
		writeKinesisErr(w, err)
		return
	}

	rendered := make([]map[string]any, 0, len(dests))
	for i := range dests {
		rendered = append(rendered, kinesisDestinationBlock(&dests[i]))
	}

	wire.WriteJSON(w, map[string]any{
		"TableName":                     req.TableName,
		"KinesisDataStreamDestinations": rendered,
	})
}

// writeKinesisDestination renders the response shared by the enable/disable/update
// operations: the table, the stream and the resulting destination status.
func writeKinesisDestination(w http.ResponseWriter, table string, dest dbdriver.KinesisDestination) {
	resp := map[string]any{
		"TableName":         table,
		"StreamArn":         dest.StreamArn,
		"DestinationStatus": dest.Status,
	}
	if dest.Precision != "" {
		resp["EnableKinesisStreamingConfiguration"] = map[string]any{
			"ApproximateCreationDateTimePrecision": dest.Precision,
		}
	}

	wire.WriteJSON(w, resp)
}

// kinesisDestinationBlock renders one destination inside a
// DescribeKinesisStreamingDestination response.
func kinesisDestinationBlock(dest *dbdriver.KinesisDestination) map[string]any {
	block := map[string]any{
		"StreamArn":         dest.StreamArn,
		"DestinationStatus": dest.Status,
	}
	if dest.Precision != "" {
		block["ApproximateCreationDateTimePrecision"] = dest.Precision
	}

	return block
}

// writeKinesisErr maps a provider error to the DynamoDB exception the Kinesis
// streaming operations return: a not-found (missing table or destination)
// becomes ResourceNotFoundException.
func writeKinesisErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", errMessage(err))
		return
	}

	writeErr(w, err)
}
