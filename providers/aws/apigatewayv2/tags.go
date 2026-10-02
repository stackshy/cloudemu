package apigatewayv2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// Resource path shapes inside an apigatewayv2 ARN
// (arn:aws:apigateway:{region}::{path}).
const (
	arnPathAPIs        = "apis"
	arnPathStages      = "stages"
	arnPathDomainNames = "domainnames"
	arnPathVpcLinks    = "vpclinks"
)

// Segment counts of the taggable resource paths.
const (
	segsTagAPI   = 2 // apis/{apiId}
	segsTagStage = 4 // apis/{apiId}/stages/{stageName}
	segsTagOther = 2 // domainnames/{name} or vpclinks/{id}
)

// arnParts is the number of colon-separated fields in an ARN.
const arnParts = 6

// tagHolder is a locked API or stage whose tags a tagging call reads or
// replaces. The caller must call unlock.
type tagHolder struct {
	api    *driver.API
	stage  *driver.Stage
	unlock func()
}

func (h tagHolder) get() map[string]string {
	if h.stage != nil {
		return h.stage.Tags
	}

	return h.api.Tags
}

func (h tagHolder) set(tags map[string]string) {
	if h.stage != nil {
		h.stage.Tags = tags
		return
	}

	h.api.Tags = tags
}

// tagTarget locks the resource an ARN names and returns it.
func (m *Mock) tagTarget(resourceARN string) (tagHolder, error) {
	path, err := m.parseARN(resourceARN)
	if err != nil {
		return tagHolder{}, err
	}

	segs := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case segs[0] == arnPathAPIs && (len(segs) == segsTagAPI || (len(segs) == segsTagStage && segs[2] == arnPathStages)):
		return m.apiOrStageTags(segs)
	case segs[0] == arnPathDomainNames && len(segs) == segsTagOther:
		return tagHolder{}, cerrors.Newf(cerrors.NotFound, "Invalid domain name identifier specified %s", segs[1])
	case segs[0] == arnPathVpcLinks && len(segs) == segsTagOther:
		return tagHolder{}, cerrors.Newf(cerrors.NotFound, "Invalid VpcLink identifier specified %s", segs[1])
	default:
		return tagHolder{}, badRequest("Invalid resource ARN specified %s", resourceARN)
	}
}

// apiOrStageTags locks the API named by segs and returns the API or stage.
func (m *Mock) apiOrStageTags(segs []string) (tagHolder, error) {
	ad, err := m.getAPI(segs[1])
	if err != nil {
		return tagHolder{}, err
	}

	ad.mu.Lock()

	if len(segs) == segsTagAPI {
		return tagHolder{api: &ad.api, unlock: ad.mu.Unlock}, nil
	}

	st, ok := ad.stages[segs[3]]
	if !ok {
		ad.mu.Unlock()

		return tagHolder{}, cerrors.Newf(cerrors.NotFound, "Invalid stage name specified %s", segs[3])
	}

	return tagHolder{stage: st, unlock: ad.mu.Unlock}, nil
}

// parseARN checks an apigateway ARN in this region and returns its resource
// path.
func (m *Mock) parseARN(resourceARN string) (string, error) {
	parts := strings.SplitN(resourceARN, ":", arnParts)
	if len(parts) != arnParts || parts[0] != "arn" || parts[2] != "apigateway" || !strings.HasPrefix(parts[5], "/") {
		return "", badRequest("Invalid resource ARN specified %s", resourceARN)
	}

	if parts[3] != m.region {
		return "", cerrors.Newf(cerrors.NotFound, "Invalid resource ARN specified %s", resourceARN)
	}

	return parts[5], nil
}

// TagResource adds or overwrites tags on an API, stage, domain name or VPC link.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	if err := validateTags(tags); err != nil {
		return err
	}

	h, err := m.tagTarget(resourceARN)
	if err != nil {
		return err
	}
	defer h.unlock()

	merged := copyStrMap(h.get())
	if merged == nil {
		merged = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		merged[k] = v
	}

	if err := validateTags(merged); err != nil {
		return err
	}

	h.set(merged)

	return nil
}

// UntagResource removes tag keys from a resource. Unknown keys are ignored.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	if len(tagKeys) == 0 {
		return badRequest("TagKeys is required")
	}

	h, err := m.tagTarget(resourceARN)
	if err != nil {
		return err
	}
	defer h.unlock()

	tags := h.get()
	for _, k := range tagKeys {
		delete(tags, k)
	}

	return nil
}

// GetTags returns a resource's tags (an empty map when it has none).
func (m *Mock) GetTags(_ context.Context, resourceARN string) (map[string]string, error) {
	h, err := m.tagTarget(resourceARN)
	if err != nil {
		return nil, err
	}
	defer h.unlock()

	out := copyStrMap(h.get())
	if out == nil {
		out = map[string]string{}
	}

	return out, nil
}
