package cloudwatch

// This file implements the CloudWatch metric-stream operations (PutMetricStream,
// GetMetricStream, ListMetricStreams, DeleteMetricStream, StartMetricStreams,
// StopMetricStreams) over the rpc-v2-cbor protocol, backing the
// aws_cloudwatch_metric_stream Terraform resource. The store is an AWS-local
// optional capability so the shared Monitoring interface — and the Azure/GCP
// providers — stay unchanged.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// metricStreamStore is the AWS-local capability behind the metric-stream
// operations.
type metricStreamStore interface {
	PutMetricStream(ctx context.Context, cfg mondriver.MetricStreamConfig) (string, error)
	GetMetricStream(ctx context.Context, name string) (*mondriver.MetricStreamInfo, error)
	ListMetricStreams(ctx context.Context) ([]mondriver.MetricStreamEntry, error)
	DeleteMetricStream(ctx context.Context, name string) error
	StartMetricStreams(ctx context.Context, names []string) error
	StopMetricStreams(ctx context.Context, names []string) error
}

// metricStreamTagger is the AWS-local capability behind the metric-stream tag
// operations, routed to from TagResource/UntagResource/ListTagsForResource by
// ARN shape (see metricStreamNameFromARN).
type metricStreamTagger interface {
	AddMetricStreamTags(ctx context.Context, name string, tags map[string]string) error
	RemoveMetricStreamTags(ctx context.Context, name string, keys []string) error
	MetricStreamTags(ctx context.Context, name string) (map[string]string, error)
}

// metricStreamFilterCBR mirrors the wire shape of a MetricStreamFilter.
type metricStreamFilterCBR struct {
	Namespace   string   `cbor:"Namespace,omitempty"`
	MetricNames []string `cbor:"MetricNames,omitempty"`
}

type metricStreamStatisticsMetricCBR struct {
	Namespace  string `cbor:"Namespace,omitempty"`
	MetricName string `cbor:"MetricName,omitempty"`
}

type metricStreamStatisticsConfigCBR struct {
	IncludeMetrics       []metricStreamStatisticsMetricCBR `cbor:"IncludeMetrics,omitempty"`
	AdditionalStatistics []string                          `cbor:"AdditionalStatistics,omitempty"`
}

type putMetricStreamInput struct {
	Name                         string                            `cbor:"Name"`
	FirehoseArn                  string                            `cbor:"FirehoseArn"`
	RoleArn                      string                            `cbor:"RoleArn"`
	OutputFormat                 string                            `cbor:"OutputFormat"`
	IncludeFilters               []metricStreamFilterCBR           `cbor:"IncludeFilters,omitempty"`
	ExcludeFilters               []metricStreamFilterCBR           `cbor:"ExcludeFilters,omitempty"`
	StatisticsConfigurations     []metricStreamStatisticsConfigCBR `cbor:"StatisticsConfigurations,omitempty"`
	IncludeLinkedAccountsMetrics bool                              `cbor:"IncludeLinkedAccountsMetrics,omitempty"`
	Tags                         []tagCBR                          `cbor:"Tags,omitempty"`
}

type putMetricStreamOutput struct {
	Arn string `cbor:"Arn"`
}

func (h *Handler) putMetricStream(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "metric streams not supported")
		return
	}

	var in putMetricStreamInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	arn, err := store.PutMetricStream(r.Context(), mondriver.MetricStreamConfig{
		Name:                         in.Name,
		FirehoseARN:                  in.FirehoseArn,
		RoleARN:                      in.RoleArn,
		OutputFormat:                 in.OutputFormat,
		IncludeFilters:               fromMetricStreamFilterCBRs(in.IncludeFilters),
		ExcludeFilters:               fromMetricStreamFilterCBRs(in.ExcludeFilters),
		StatisticsConfigurations:     fromMetricStreamStatisticsConfigCBRs(in.StatisticsConfigurations),
		IncludeLinkedAccountsMetrics: in.IncludeLinkedAccountsMetrics,
		Tags:                         tagsToMap(in.Tags),
	})
	if err != nil {
		writeMetricStreamDriverErr(w, err)
		return
	}

	writeCBORResponse(w, putMetricStreamOutput{Arn: arn})
}

type getMetricStreamInput struct {
	Name string `cbor:"Name"`
}

type getMetricStreamOutput struct {
	Arn                          string                            `cbor:"Arn,omitempty"`
	Name                         string                            `cbor:"Name,omitempty"`
	FirehoseArn                  string                            `cbor:"FirehoseArn,omitempty"`
	RoleArn                      string                            `cbor:"RoleArn,omitempty"`
	OutputFormat                 string                            `cbor:"OutputFormat,omitempty"`
	State                        string                            `cbor:"State,omitempty"`
	IncludeFilters               []metricStreamFilterCBR           `cbor:"IncludeFilters,omitempty"`
	ExcludeFilters               []metricStreamFilterCBR           `cbor:"ExcludeFilters,omitempty"`
	StatisticsConfigurations     []metricStreamStatisticsConfigCBR `cbor:"StatisticsConfigurations,omitempty"`
	IncludeLinkedAccountsMetrics bool                              `cbor:"IncludeLinkedAccountsMetrics,omitempty"`
	CreationDate                 time.Time                         `cbor:"CreationDate,omitempty"`
	LastUpdateDate               time.Time                         `cbor:"LastUpdateDate,omitempty"`
}

func (h *Handler) getMetricStream(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "metric streams not supported")
		return
	}

	var in getMetricStreamInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	s, err := store.GetMetricStream(r.Context(), in.Name)
	if err != nil {
		writeMetricStreamDriverErr(w, err)
		return
	}

	writeCBORResponse(w, getMetricStreamOutput{
		Arn:                          s.ARN,
		Name:                         s.Name,
		FirehoseArn:                  s.FirehoseARN,
		RoleArn:                      s.RoleARN,
		OutputFormat:                 s.OutputFormat,
		State:                        s.State,
		IncludeFilters:               toMetricStreamFilterCBRs(s.IncludeFilters),
		ExcludeFilters:               toMetricStreamFilterCBRs(s.ExcludeFilters),
		StatisticsConfigurations:     toMetricStreamStatisticsConfigCBRs(s.StatisticsConfigurations),
		IncludeLinkedAccountsMetrics: s.IncludeLinkedAccountsMetrics,
		CreationDate:                 s.CreationDate.UTC(),
		LastUpdateDate:               s.LastUpdateDate.UTC(),
	})
}

type listMetricStreamsInput struct {
	MaxResults int    `cbor:"MaxResults,omitempty"`
	NextToken  string `cbor:"NextToken,omitempty"`
}

type metricStreamEntryCBR struct {
	Arn            string    `cbor:"Arn,omitempty"`
	Name           string    `cbor:"Name,omitempty"`
	FirehoseArn    string    `cbor:"FirehoseArn,omitempty"`
	OutputFormat   string    `cbor:"OutputFormat,omitempty"`
	State          string    `cbor:"State,omitempty"`
	CreationDate   time.Time `cbor:"CreationDate,omitempty"`
	LastUpdateDate time.Time `cbor:"LastUpdateDate,omitempty"`
}

type listMetricStreamsOutput struct {
	Entries   []metricStreamEntryCBR `cbor:"Entries"`
	NextToken string                 `cbor:"NextToken,omitempty"`
}

// listMetricStreamsPageSize is the number of metric-stream entries returned
// per page, matching real CloudWatch's documented MaxResults ceiling.
const listMetricStreamsPageSize = 500

func (h *Handler) listMetricStreams(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "metric streams not supported")
		return
	}

	var in listMetricStreamsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	entries, err := store.ListMetricStreams(r.Context())
	if err != nil {
		writeMetricStreamDriverErr(w, err)
		return
	}

	size := listMetricStreamsPageSize
	if in.MaxResults > 0 && in.MaxResults < size {
		size = in.MaxResults
	}

	from, to, next := pageWindow(len(entries), decodeOffsetToken(in.NextToken), size)

	rows := make([]metricStreamEntryCBR, 0, to-from)

	for i := from; i < to; i++ {
		e := &entries[i]
		rows = append(rows, metricStreamEntryCBR{
			Arn:            e.ARN,
			Name:           e.Name,
			FirehoseArn:    e.FirehoseARN,
			OutputFormat:   e.OutputFormat,
			State:          e.State,
			CreationDate:   e.CreationDate.UTC(),
			LastUpdateDate: e.LastUpdateDate.UTC(),
		})
	}

	resp := listMetricStreamsOutput{Entries: rows}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	writeCBORResponse(w, resp)
}

type deleteMetricStreamInput struct {
	Name string `cbor:"Name"`
}

// deleteMetricStream is structurally identical to deleteDashboards (unmarshal
// a single-field input, call the matching AWS-local store method, write an
// empty success response) — the two resources' delete semantics genuinely
// share this shape, so the duplication is inherent rather than a missed
// abstraction.
//
//nolint:dupl // structurally identical to deleteDashboards; see comment above.
func (h *Handler) deleteMetricStream(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "metric streams not supported")
		return
	}

	var in deleteMetricStreamInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := store.DeleteMetricStream(r.Context(), in.Name); err != nil {
		writeMetricStreamDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

type metricStreamNamesInput struct {
	Names []string `cbor:"Names,omitempty"`
}

func (h *Handler) startMetricStreams(w http.ResponseWriter, r *http.Request, body []byte) {
	h.setMetricStreamsRunning(w, r, body, true)
}

func (h *Handler) stopMetricStreams(w http.ResponseWriter, r *http.Request, body []byte) {
	h.setMetricStreamsRunning(w, r, body, false)
}

func (h *Handler) setMetricStreamsRunning(w http.ResponseWriter, r *http.Request, body []byte, running bool) {
	store, ok := h.monitoring.(metricStreamStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "metric streams not supported")
		return
	}

	var in metricStreamNamesInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	var err error
	if running {
		err = store.StartMetricStreams(r.Context(), in.Names)
	} else {
		err = store.StopMetricStreams(r.Context(), in.Names)
	}

	if err != nil {
		writeMetricStreamDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}

// writeMetricStreamDriverErr maps a metric-stream driver error to the real
// CloudWatch error shape names these operations document — ResourceNotFoundException
// (GetMetricStream) and InvalidParameterValueException (PutMetricStream) —
// which carry the "Exception" suffix that the shared writeDriverErr's shorter
// names (used by the older alarm operations) drop. The exact name matters: an
// SDK/Terraform delete-waiter matches on the deserialized error code, and a
// mismatched name looks like an unexpected failure rather than a signal that
// the resource is gone.
func writeMetricStreamDriverErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeCBORError(w, http.StatusNotFound, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeCBORError(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
	default:
		writeDriverErr(w, err)
	}
}

// metricStreamNameFromARN extracts a metric stream's name from an ARN of the
// form arn:aws:cloudwatch:region:account:metric-stream/NAME. It reports false
// for any ARN that doesn't carry that resource segment (including a bare
// alarm name or an alarm ARN), so TagResource/UntagResource/ListTagsForResource
// can fall back to alarm tag routing.
func metricStreamNameFromARN(arn string) (string, bool) {
	const marker = ":metric-stream/"

	i := strings.Index(arn, marker)
	if i < 0 {
		return "", false
	}

	return arn[i+len(marker):], true
}

func fromMetricStreamFilterCBRs(in []metricStreamFilterCBR) []mondriver.MetricStreamFilter {
	if len(in) == 0 {
		return nil
	}

	out := make([]mondriver.MetricStreamFilter, 0, len(in))
	for _, f := range in {
		out = append(out, mondriver.MetricStreamFilter{Namespace: f.Namespace, MetricNames: f.MetricNames})
	}

	return out
}

func toMetricStreamFilterCBRs(in []mondriver.MetricStreamFilter) []metricStreamFilterCBR {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricStreamFilterCBR, 0, len(in))
	for _, f := range in {
		out = append(out, metricStreamFilterCBR{Namespace: f.Namespace, MetricNames: f.MetricNames})
	}

	return out
}

func fromMetricStreamStatisticsConfigCBRs(in []metricStreamStatisticsConfigCBR) []mondriver.MetricStreamStatisticsConfig {
	if len(in) == 0 {
		return nil
	}

	out := make([]mondriver.MetricStreamStatisticsConfig, 0, len(in))

	for _, c := range in {
		metrics := make([]mondriver.MetricStreamStatisticsMetric, 0, len(c.IncludeMetrics))
		for _, mtr := range c.IncludeMetrics {
			metrics = append(metrics, mondriver.MetricStreamStatisticsMetric{Namespace: mtr.Namespace, MetricName: mtr.MetricName})
		}

		out = append(out, mondriver.MetricStreamStatisticsConfig{
			IncludeMetrics:       metrics,
			AdditionalStatistics: c.AdditionalStatistics,
		})
	}

	return out
}

func toMetricStreamStatisticsConfigCBRs(in []mondriver.MetricStreamStatisticsConfig) []metricStreamStatisticsConfigCBR {
	if len(in) == 0 {
		return nil
	}

	out := make([]metricStreamStatisticsConfigCBR, 0, len(in))

	for _, c := range in {
		metrics := make([]metricStreamStatisticsMetricCBR, 0, len(c.IncludeMetrics))
		for _, mtr := range c.IncludeMetrics {
			metrics = append(metrics, metricStreamStatisticsMetricCBR{Namespace: mtr.Namespace, MetricName: mtr.MetricName})
		}

		out = append(out, metricStreamStatisticsConfigCBR{
			IncludeMetrics:       metrics,
			AdditionalStatistics: c.AdditionalStatistics,
		})
	}

	return out
}
