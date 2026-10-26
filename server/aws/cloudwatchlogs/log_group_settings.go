package cloudwatchlogs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// maxListLogGroupsLimit is the largest ListLogGroups limit AWS accepts.
const maxListLogGroupsLimit = 1000

func (h *Handler) putRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "PutRetentionPolicy")
	if !ok {
		return
	}

	var req struct {
		LogGroupName    string `json:"logGroupName"`
		RetentionInDays int    `json:"retentionInDays"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.PutRetentionPolicy(r.Context(), req.LogGroupName, req.RetentionInDays); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) deleteRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DeleteRetentionPolicy")
	if !ok {
		return
	}

	var req struct {
		LogGroupName string `json:"logGroupName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.DeleteRetentionPolicy(r.Context(), req.LogGroupName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

type logGroupTagFilterJSON struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

type listLogGroupsRequest struct {
	LogGroupNamePattern string                  `json:"logGroupNamePattern"`
	LogGroupClass       string                  `json:"logGroupClass"`
	LogGroupTags        []logGroupTagFilterJSON `json:"logGroupTags"`
	Limit               int32                   `json:"limit"`
	NextToken           string                  `json:"nextToken"`
}

type logGroupSummaryJSON struct {
	LogGroupName  string `json:"logGroupName"`
	LogGroupArn   string `json:"logGroupArn"`
	LogGroupClass string `json:"logGroupClass"`
}

type listLogGroupsResponse struct {
	LogGroups []logGroupSummaryJSON `json:"logGroups"`
	NextToken string                `json:"nextToken,omitempty"`
}

// listLogGroups serves the account-level ListLogGroups. Linked-account
// parameters are accepted and ignored: there is a single account here.
func (h *Handler) listLogGroups(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "ListLogGroups")
	if !ok {
		return
	}

	var req listLogGroupsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.Limit < 0 || req.Limit > maxListLogGroupsLimit {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'limit' failed to satisfy constraint: "+
				"Member must have value between 1 and 1000")

		return
	}

	filter := logdriver.ListLogGroupsFilter{
		NamePattern:   req.LogGroupNamePattern,
		LogGroupClass: req.LogGroupClass,
	}

	for _, t := range req.LogGroupTags {
		filter.Tags = append(filter.Tags, logdriver.LogGroupTagFilter{Key: t.Key, Values: t.Values})
	}

	groups, err := ext.ListAWSLogGroups(r.Context(), filter)
	if err != nil {
		writeErr(w, err)
		return
	}

	from, to, next := pageBounds(len(groups), decodeOffsetToken(req.NextToken), req.Limit)

	resp := listLogGroupsResponse{LogGroups: make([]logGroupSummaryJSON, 0, to-from)}
	for i := from; i < to; i++ {
		resp.LogGroups = append(resp.LogGroups, logGroupSummaryJSON{
			LogGroupName:  groups[i].Name,
			LogGroupArn:   groups[i].ResourceID,
			LogGroupClass: groups[i].LogGroupClass,
		})
	}

	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

type kmsKeyRequest struct {
	LogGroupName       string `json:"logGroupName"`
	ResourceIdentifier string `json:"resourceIdentifier"`
	KmsKeyID           string `json:"kmsKeyId"`
}

func (h *Handler) associateKmsKey(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "AssociateKmsKey")
	if !ok {
		return
	}

	var req kmsKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.AssociateKmsKey(r.Context(), req.LogGroupName, req.ResourceIdentifier, req.KmsKeyID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) disassociateKmsKey(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DisassociateKmsKey")
	if !ok {
		return
	}

	var req kmsKeyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.DisassociateKmsKey(r.Context(), req.LogGroupName, req.ResourceIdentifier); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
