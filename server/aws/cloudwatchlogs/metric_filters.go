package cloudwatchlogs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// metricTransformationJSON is one MetricTransformation, the object that binds a
// matched log event to an emitted CloudWatch metric. AWS fixes the enclosing
// array to exactly one element, so this maps 1:1 onto the flat driver config.
type metricTransformationJSON struct {
	MetricName      string            `json:"metricName"`
	MetricNamespace string            `json:"metricNamespace"`
	MetricValue     string            `json:"metricValue"`
	DefaultValue    *float64          `json:"defaultValue,omitempty"`
	Dimensions      map[string]string `json:"dimensions,omitempty"`
	Unit            string            `json:"unit,omitempty"`
}

type putMetricFilterRequest struct {
	LogGroupName          string                     `json:"logGroupName"`
	FilterName            string                     `json:"filterName"`
	FilterPattern         string                     `json:"filterPattern"`
	MetricTransformations []metricTransformationJSON `json:"metricTransformations"`
}

type describeMetricFiltersRequest struct {
	LogGroupName     string `json:"logGroupName"`
	FilterNamePrefix string `json:"filterNamePrefix"`
	MetricName       string `json:"metricName"`
	MetricNamespace  string `json:"metricNamespace"`
	Limit            int32  `json:"limit"`
	NextToken        string `json:"nextToken"`
}

type deleteMetricFilterRequest struct {
	LogGroupName string `json:"logGroupName"`
	FilterName   string `json:"filterName"`
}

// metricFilterJSON is a DescribeMetricFilters response element.
type metricFilterJSON struct {
	FilterName            string                     `json:"filterName"`
	FilterPattern         string                     `json:"filterPattern"`
	LogGroupName          string                     `json:"logGroupName"`
	CreationTime          int64                      `json:"creationTime"`
	MetricTransformations []metricTransformationJSON `json:"metricTransformations"`
}

type describeMetricFiltersResponse struct {
	MetricFilters []metricFilterJSON `json:"metricFilters"`
	NextToken     string             `json:"nextToken,omitempty"`
}

// putMetricFilter creates or updates a metric filter (Logs_20140328.PutMetricFilter).
// The metricTransformations array is fixed at one element by AWS, so the first
// transformation supplies the metric this filter emits to. A successful call
// returns an empty body.
func (h *Handler) putMetricFilter(w http.ResponseWriter, r *http.Request) {
	var req putMetricFilterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if len(req.MetricTransformations) == 0 {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"metricTransformations is required and must contain exactly one item")
		return
	}

	mt := req.MetricTransformations[0]

	if err := h.logs.PutMetricFilter(r.Context(), &logdriver.MetricFilterConfig{
		Name:            req.FilterName,
		LogGroup:        req.LogGroupName,
		FilterPattern:   req.FilterPattern,
		MetricName:      mt.MetricName,
		MetricNamespace: mt.MetricNamespace,
		MetricValue:     mt.MetricValue,
		DefaultValue:    mt.DefaultValue,
		Unit:            mt.Unit,
		Dimensions:      mt.Dimensions,
	}); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// describeMetricFilters lists a log group's metric filters, ASCII-sorted by
// filter name and optionally narrowed by filterNamePrefix.
func (h *Handler) describeMetricFilters(w http.ResponseWriter, r *http.Request) {
	var req describeMetricFiltersRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	filters, err := h.logs.DescribeMetricFilters(r.Context(), req.LogGroupName)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]metricFilterJSON, 0, len(filters))

	for i := range filters {
		mf := &filters[i]
		if req.FilterNamePrefix != "" && !strings.HasPrefix(mf.Name, req.FilterNamePrefix) {
			continue
		}

		out = append(out, metricFilterJSON{
			FilterName:    mf.Name,
			FilterPattern: mf.FilterPattern,
			LogGroupName:  mf.LogGroup,
			CreationTime:  epochMillis(mf.CreatedAt),
			MetricTransformations: []metricTransformationJSON{{
				MetricName:      mf.MetricName,
				MetricNamespace: mf.MetricNamespace,
				MetricValue:     mf.MetricValue,
				DefaultValue:    mf.DefaultValue,
				Unit:            mf.Unit,
				Dimensions:      mf.Dimensions,
			}},
		})
	}

	wire.WriteJSON(w, describeMetricFiltersResponse{MetricFilters: out})
}

// deleteMetricFilter removes a metric filter from a log group. A successful
// call returns an empty body.
func (h *Handler) deleteMetricFilter(w http.ResponseWriter, r *http.Request) {
	var req deleteMetricFilterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.logs.DeleteMetricFilter(r.Context(), req.LogGroupName, req.FilterName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
