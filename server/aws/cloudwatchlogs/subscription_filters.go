package cloudwatchlogs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

type putSubscriptionFilterRequest struct {
	LogGroupName   string `json:"logGroupName"`
	FilterName     string `json:"filterName"`
	FilterPattern  string `json:"filterPattern"`
	DestinationArn string `json:"destinationArn"`
	RoleArn        string `json:"roleArn"`
	Distribution   string `json:"distribution"`
}

type describeSubscriptionFiltersRequest struct {
	LogGroupName     string `json:"logGroupName"`
	FilterNamePrefix string `json:"filterNamePrefix"`
	Limit            int32  `json:"limit"`
	NextToken        string `json:"nextToken"`
}

type deleteSubscriptionFilterRequest struct {
	LogGroupName string `json:"logGroupName"`
	FilterName   string `json:"filterName"`
}

// subscriptionFilterJSON is a DescribeSubscriptionFilters response element.
type subscriptionFilterJSON struct {
	FilterName     string `json:"filterName"`
	LogGroupName   string `json:"logGroupName"`
	FilterPattern  string `json:"filterPattern"`
	DestinationArn string `json:"destinationArn"`
	RoleArn        string `json:"roleArn,omitempty"`
	Distribution   string `json:"distribution,omitempty"`
	CreationTime   int64  `json:"creationTime"`
}

type describeSubscriptionFiltersResponse struct {
	SubscriptionFilters []subscriptionFilterJSON `json:"subscriptionFilters"`
	NextToken           string                   `json:"nextToken,omitempty"`
}

// putSubscriptionFilter creates or updates a subscription filter
// (Logs_20140328.PutSubscriptionFilter). A successful call returns an empty body.
func (h *Handler) putSubscriptionFilter(w http.ResponseWriter, r *http.Request) {
	var req putSubscriptionFilterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.logs.PutSubscriptionFilter(r.Context(), &logdriver.SubscriptionFilterConfig{
		Name:           req.FilterName,
		LogGroup:       req.LogGroupName,
		FilterPattern:  req.FilterPattern,
		DestinationARN: req.DestinationArn,
		RoleARN:        req.RoleArn,
		Distribution:   req.Distribution,
	}); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// describeSubscriptionFilters lists a log group's subscription filters,
// ASCII-sorted by filter name and optionally narrowed by filterNamePrefix.
func (h *Handler) describeSubscriptionFilters(w http.ResponseWriter, r *http.Request) {
	var req describeSubscriptionFiltersRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	filters, err := h.logs.DescribeSubscriptionFilters(r.Context(), req.LogGroupName)
	if err != nil {
		writeErr(w, err)
		return
	}

	matched := make([]logdriver.SubscriptionFilterInfo, 0, len(filters))

	for i := range filters {
		if req.FilterNamePrefix != "" && !strings.HasPrefix(filters[i].Name, req.FilterNamePrefix) {
			continue
		}

		matched = append(matched, filters[i])
	}

	from, to, next := pageBounds(len(matched), decodeOffsetToken(req.NextToken), req.Limit)

	out := make([]subscriptionFilterJSON, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, subscriptionFilterJSON{
			FilterName:     matched[i].Name,
			LogGroupName:   matched[i].LogGroup,
			FilterPattern:  matched[i].FilterPattern,
			DestinationArn: matched[i].DestinationARN,
			RoleArn:        matched[i].RoleARN,
			Distribution:   matched[i].Distribution,
			CreationTime:   epochMillis(matched[i].CreatedAt),
		})
	}

	resp := describeSubscriptionFiltersResponse{SubscriptionFilters: out}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

// deleteSubscriptionFilter removes a subscription filter from a log group. A
// successful call returns an empty body.
func (h *Handler) deleteSubscriptionFilter(w http.ResponseWriter, r *http.Request) {
	var req deleteSubscriptionFilterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.logs.DeleteSubscriptionFilter(r.Context(), req.LogGroupName, req.FilterName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
