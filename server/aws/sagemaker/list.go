package sagemaker

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// defaultListPageSize is the page size a List call gets when the request has
// no MaxResults, matching SageMaker's default of 10.
const defaultListPageSize = 10

// listReq is the union of the filter/sort/paging fields the SageMaker List
// operations accept. Each operation reads only the fields its SDK input
// defines; the rest stay at their zero value. Timestamps arrive as epoch
// seconds (awsJson1_1).
type listReq struct {
	NameContains             string   `json:"NameContains"`
	PipelineNamePrefix       string   `json:"PipelineNamePrefix"`
	StatusEquals             string   `json:"StatusEquals"`
	FeatureGroupStatusEqual  string   `json:"FeatureGroupStatusEquals"`
	CreationTimeAfter        *float64 `json:"CreationTimeAfter"`
	CreationTimeBefore       *float64 `json:"CreationTimeBefore"`
	CreatedAfter             *float64 `json:"CreatedAfter"`
	CreatedBefore            *float64 `json:"CreatedBefore"`
	LastModifiedTimeAfter    *float64 `json:"LastModifiedTimeAfter"`
	LastModifiedTimeBefore   *float64 `json:"LastModifiedTimeBefore"`
	SortBy                   string   `json:"SortBy"`
	SortOrder                string   `json:"SortOrder"`
	MaxResults               *int     `json:"MaxResults"`
	NextToken                string   `json:"NextToken"`
	ModelApprovalStatus      string   `json:"ModelApprovalStatus"`
	ModelPackageGroupName    string   `json:"ModelPackageGroupName"`
	DomainIDEquals           string   `json:"DomainIdEquals"`
	PipelineName             string   `json:"PipelineName"`
	ExperimentName           string   `json:"ExperimentName"`
	WorkteamArn              string   `json:"WorkteamArn"`
	JobReferenceCodeContains string   `json:"JobReferenceCodeContains"`
	ImageName                string   `json:"ImageName"`
	TrialName                string   `json:"TrialName"`
	SourceArn                string   `json:"SourceArn"`
	SourceURI                string   `json:"SourceUri"`
	ArtifactType             string   `json:"ArtifactType"`
	ActionType               string   `json:"ActionType"`
	ContextType              string   `json:"ContextType"`
	AssociationType          string   `json:"AssociationType"`
	DestinationArn           string   `json:"DestinationArn"`
	SourceType               string   `json:"SourceType"`
	DestinationType          string   `json:"DestinationType"`
	Alias                    string   `json:"Alias"`
	Version                  *int     `json:"Version"`
	ModifiedTimeAfter        *float64 `json:"ModifiedTimeAfter"`
	ModifiedTimeBefore       *float64 `json:"ModifiedTimeBefore"`
}

// decodeListReq reads a List request body and rejects an explicit MaxResults
// of zero (the range is 1-100); validation of the rest lives in the provider.
func decodeListReq(w http.ResponseWriter, r *http.Request) (listReq, bool) {
	var req listReq
	if !wire.DecodeJSON(w, r, &req) {
		return listReq{}, false
	}

	if req.MaxResults != nil && *req.MaxResults == 0 {
		writeDriverError(w, errors.New(errors.InvalidArgument,
			"1 validation error detected: Value '0' at 'maxResults' failed to satisfy constraint: "+
				"Member must have value between 1 and 100"))

		return listReq{}, false
	}

	return req, true
}

// options converts the request into driver ListOptions.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (q listReq) options() driver.ListOptions {
	opts := driver.ListOptions{
		NameContains:           q.NameContains,
		NamePrefix:             q.PipelineNamePrefix,
		StatusEquals:           firstNonEmpty(q.StatusEquals, q.FeatureGroupStatusEqual),
		CreationTimeAfter:      isoFrom(q.CreationTimeAfter, q.CreatedAfter),
		CreationTimeBefore:     isoFrom(q.CreationTimeBefore, q.CreatedBefore),
		LastModifiedTimeAfter:  isoFrom(q.LastModifiedTimeAfter, q.ModifiedTimeAfter),
		LastModifiedTimeBefore: isoFrom(q.LastModifiedTimeBefore, q.ModifiedTimeBefore),
		SortBy:                 sortKey(q.SortBy),
		SortOrder:              sortOrderKey(q.SortOrder),
		MaxResults:             defaultListPageSize,
		NextToken:              q.NextToken,
	}

	if q.MaxResults != nil {
		opts.MaxResults = *q.MaxResults
	}

	opts.Filters = map[string]string{}

	for k, v := range map[string]string{
		"ModelApprovalStatus": q.ModelApprovalStatus, "JobReferenceCodeContains": q.JobReferenceCodeContains,
		"ExperimentName": q.ExperimentName, "TrialName": q.TrialName, "SourceArn": q.SourceArn, "SourceUri": q.SourceURI,
		"ArtifactType": q.ArtifactType, "ActionType": q.ActionType, "ContextType": q.ContextType,
		"AssociationType": q.AssociationType, "DestinationArn": q.DestinationArn, "SourceType": q.SourceType,
		"DestinationType": q.DestinationType,
	} {
		if v != "" {
			opts.Filters[k] = v
		}
	}

	return opts
}

// sortKey maps the SortBy values some operations spell differently
// (CreateDate, CREATION_TIME, NAME) onto the shared keys.
func sortKey(v string) string {
	switch v {
	case "CreateDate", "CREATION_TIME":
		return driver.SortByCreationTime
	case "LAST_MODIFIED_TIME":
		return driver.SortByLastModifiedTime
	case "NAME", "IMAGE_NAME", "VERSION":
		return driver.SortByName
	default:
		return v
	}
}

// sortOrderKey maps the upper-case SortOrder spelling some operations use.
func sortOrderKey(v string) string {
	switch v {
	case "ASCENDING":
		return driver.SortAscending
	case "DESCENDING":
		return driver.SortDescending
	default:
		return v
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

// isoFrom returns the RFC3339Nano form of the first non-nil epoch-seconds
// value, or "" when none is set.
func isoFrom(vals ...*float64) string {
	for _, v := range vals {
		if v != nil {
			sec := int64(*v)
			nsec := int64((*v - float64(sec)) * float64(time.Second))

			return time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
		}
	}

	return ""
}
