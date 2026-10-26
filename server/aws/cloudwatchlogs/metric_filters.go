package cloudwatchlogs

import (
	"net/http"

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

	if len(req.MetricTransformations) != 1 {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'metricTransformations' failed to satisfy constraint: "+
				"Member must have length less than or equal to 1 and greater than or equal to 1")

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

// maxDescribeMetricFiltersLimit is the largest DescribeMetricFilters limit.
const maxDescribeMetricFiltersLimit = 50

// describeMetricFilters lists metric filters (Logs_20140328.DescribeMetricFilters),
// for one log group or the whole account, ASCII-sorted by filter name and
// narrowed by filterNamePrefix or by metricName plus metricNamespace.
func (h *Handler) describeMetricFilters(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DescribeMetricFilters")
	if !ok {
		return
	}

	var req describeMetricFiltersRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.Limit < 0 || req.Limit > maxDescribeMetricFiltersLimit {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'limit' failed to satisfy constraint: "+
				"Member must have value between 1 and 50")

		return
	}

	filters, err := ext.DescribeAWSMetricFilters(r.Context(), logdriver.DescribeMetricFiltersInput{
		LogGroup: req.LogGroupName, FilterNamePrefix: req.FilterNamePrefix,
		MetricName: req.MetricName, MetricNamespace: req.MetricNamespace,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	from, to, next := pageBounds(len(filters), decodeOffsetToken(req.NextToken), req.Limit)

	resp := describeMetricFiltersResponse{MetricFilters: make([]metricFilterJSON, 0, to-from)}
	for i := from; i < to; i++ {
		resp.MetricFilters = append(resp.MetricFilters, toMetricFilterJSON(&filters[i]))
	}

	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func toMetricFilterJSON(mf *logdriver.MetricFilterInfo) metricFilterJSON {
	return metricFilterJSON{
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
	}
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
