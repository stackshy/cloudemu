// Package resourcegroupstaggingapi serves the AWS Resource Groups Tagging
// API JSON 1.1 protocol against a *resourcediscovery.Engine. Real
// aws-sdk-go-v2/service/resourcegroupstaggingapi clients work end-to-end.
package resourcegroupstaggingapi

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

const targetPrefix = "ResourceGroupsTaggingAPI_20170126."

// AWS service identifiers used to translate portable service names to the
// names that appear in real AWS ARN/filter syntax.
const (
	awsServiceEC2      = "ec2"
	awsServiceS3       = "s3"
	awsServiceDynamoDB = "dynamodb"
	awsServiceLambda   = "lambda"
)

// Error code returned for any tag-routing failure surfaced to the SDK
// client. Real AWS uses InvalidParameterException for both bad ARNs and
// resource-not-found.
const errInvalidParameter = "InvalidParameterException"

// Handler serves Resource Groups Tagging API JSON-RPC requests.
type Handler struct {
	engine *resourcediscovery.Engine
}

// New returns a handler backed by the given cross-service discovery engine.
func New(engine *resourcediscovery.Engine) *Handler {
	return &Handler{engine: engine}
}

// Matches returns true for an X-Amz-Target of "ResourceGroupsTaggingAPI_20170126.<Op>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches by X-Amz-Target operation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "GetResources":
		h.getResources(w, r)
	case "GetTagKeys":
		h.getTagKeys(w, r)
	case "GetTagValues":
		h.getTagValues(w, r)
	case "TagResources":
		h.tagResources(w, r)
	case "UntagResources":
		h.untagResources(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown operation: "+op)
	}
}

type tagFilter struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

func (h *Handler) getResources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceTypeFilters []string    `json:"ResourceTypeFilters"`
		TagFilters          []tagFilter `json:"TagFilters"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	all, err := h.engine.ListAll(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]map[string]any, 0, len(all))

	for i := range all {
		res := &all[i]
		if !matchesTypeFilter(res, req.ResourceTypeFilters) {
			continue
		}

		if !matchesTagFilters(res, req.TagFilters) {
			continue
		}

		out = append(out, map[string]any{
			"ResourceARN": res.ARN,
			"Tags":        toTagList(res.Tags),
		})
	}

	wire.WriteJSON(w, map[string]any{
		"ResourceTagMappingList": out,
		"PaginationToken":        "",
	})
}

func (h *Handler) getTagKeys(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	keys, err := h.engine.GetTagKeys(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TagKeys":         keys,
		"PaginationToken": "",
	})
}

func (h *Handler) getTagValues(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"Key"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.Key == "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", "Key is required")
		return
	}

	vals, err := h.engine.GetTagValues(r.Context(), req.Key)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TagValues":       vals,
		"PaginationToken": "",
	})
}

func (h *Handler) tagResources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARNList []string          `json:"ResourceARNList"`
		Tags            map[string]string `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	failed := map[string]map[string]string{}

	for _, arn := range req.ResourceARNList {
		if err := h.engine.TagResourceByARN(r.Context(), arn, req.Tags); err != nil {
			failed[arn] = map[string]string{
				"ErrorCode":    awsErrorCode(err),
				"ErrorMessage": err.Error(),
			}
		}
	}

	wire.WriteJSON(w, map[string]any{
		"FailedResourcesMap": failed,
	})
}

func (h *Handler) untagResources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARNList []string `json:"ResourceARNList"`
		TagKeys         []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	failed := map[string]map[string]string{}

	for _, arn := range req.ResourceARNList {
		if err := h.engine.UntagResourceByARN(r.Context(), arn, req.TagKeys); err != nil {
			failed[arn] = map[string]string{
				"ErrorCode":    awsErrorCode(err),
				"ErrorMessage": err.Error(),
			}
		}
	}

	wire.WriteJSON(w, map[string]any{
		"FailedResourcesMap": failed,
	})
}

func matchesTypeFilter(r *resourcediscovery.Resource, filters []string) bool {
	if len(filters) == 0 {
		return true
	}

	// Filter syntax: "service" (e.g., "s3") or "service:type" (e.g., "dynamodb:table").
	for _, f := range filters {
		svc, rt, hasType := strings.Cut(f, ":")
		// Translate portable service to AWS service name for matching.
		awsService := portableToAWSService(r.Service)
		if svc != awsService {
			continue
		}

		if !hasType {
			return true
		}

		if strings.EqualFold(rt, r.Type) {
			return true
		}
	}

	return false
}

func portableToAWSService(s string) string {
	switch s {
	case "compute", "networking":
		return awsServiceEC2
	case "storage":
		return awsServiceS3
	case "database":
		return awsServiceDynamoDB
	case "serverless":
		return awsServiceLambda
	default:
		return s
	}
}

func matchesTagFilters(r *resourcediscovery.Resource, filters []tagFilter) bool {
	for _, tf := range filters {
		got, ok := r.Tags[tf.Key]
		if !ok {
			return false
		}

		if len(tf.Values) == 0 {
			continue
		}

		matched := false

		for _, v := range tf.Values {
			if v == got {
				matched = true
				break
			}
		}

		if !matched {
			return false
		}
	}

	return true
}

func toTagList(tags map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		out = append(out, map[string]string{"Key": k, "Value": v})
	}

	return out
}

func awsErrorCode(err error) string {
	switch {
	case cerrors.IsNotFound(err), cerrors.IsInvalidArgument(err):
		return errInvalidParameter
	default:
		return "InternalServiceException"
	}
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err), cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, errInvalidParameter, err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServiceException", err.Error())
	}
}
