package ec2

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// computeTagger is the AWS-specific compute-resource tagging surface
// (instances/volumes/snapshots/images). It's not part of the portable Compute
// driver (Azure/GCP also implement it), so the handler type-asserts for it.
type computeTagger interface {
	TagResource(ctx context.Context, id string, tags map[string]string) error
	UntagResource(ctx context.Context, id string, keys []string) error
}

type tagsResponseXML struct {
	XMLName   xml.Name `xml:"CreateTagsResponse"`
	Return    bool     `xml:"return"`
	RequestID string   `xml:"requestId"`
}

type deleteTagsResponseXML struct {
	XMLName   xml.Name `xml:"DeleteTagsResponse"`
	Return    bool     `xml:"return"`
	RequestID string   `xml:"requestId"`
}

// describeTagItemXML is one <tagSet><item>…</item></tagSet> entry in a
// DescribeTags response. Unlike the resource-embedded tagItem (key/value only),
// DescribeTags reports the owning resource and its type alongside each tag.
type describeTagItemXML struct {
	ResourceID   string `xml:"resourceId"`
	ResourceType string `xml:"resourceType"`
	Key          string `xml:"key"`
	Value        string `xml:"value"`
}

type describeTagsResponseXML struct {
	XMLName   xml.Name             `xml:"DescribeTagsResponse"`
	Xmlns     string               `xml:"xmlns,attr"`
	RequestID string               `xml:"requestId"`
	TagSet    []describeTagItemXML `xml:"tagSet>item"`
	NextToken string               `xml:"nextToken,omitempty"`
}

// tagCursorSep joins a tag's resource id and key into the stable cursor/sort key
// DescribeTags pages on. A resource holds each key at most once, so the pair is
// unique; the NUL separator cannot appear in an id or key, so it never collides.
const tagCursorSep = "\x00"

// tagItemCursor is the stable per-item id DescribeTags sorts and pages on. Tags
// come from map iteration, so a composite key (not the resource id alone) is
// what makes the base64 NextToken deterministic across calls.
func tagItemCursor(it describeTagItemXML) string {
	return it.ResourceID + tagCursorSep + it.Key
}

// tagRecord is one flattened resource/tag pair gathered from the compute and
// networking drivers before filtering.
type tagRecord struct {
	resourceID   string
	resourceType string
	key          string
	value        string
}

func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateTags":
		h.createTags(w, r)
	case "DeleteTags":
		h.deleteTags(w, r)
	case "DescribeTags":
		h.describeTags(w, r)
	default:
		return false
	}

	return true
}

// describeTags reports every resource/tag pair known to the compute and VPC
// drivers, honoring the SDK's key / resource-id / resource-type / value
// filters. EC2 owns this action for EC2-scoped requests; the elbv2 handler
// scope-gates its own DescribeTags to the load-balancing credential so
// EC2-scoped calls fall through here.
func (h *Handler) describeTags(w http.ResponseWriter, r *http.Request) {
	filters := awsquery.Filters(r.Form)

	var recs []tagRecord
	recs = h.collectComputeTags(r.Context(), recs)
	recs = h.collectNetworkTags(r.Context(), recs)

	items := make([]describeTagItemXML, 0, len(recs))

	for _, rec := range recs {
		if !tagMatchesFilters(rec, filters) {
			continue
		}

		items = append(items, describeTagItemXML{
			ResourceID:   rec.resourceID,
			ResourceType: rec.resourceType,
			Key:          rec.key,
			Value:        rec.value,
		})
	}

	page, next := pageNetworkingXML(items, r, tagItemCursor)

	awsquery.WriteXMLResponse(w, describeTagsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		TagSet:    page,
		NextToken: next,
	})
}

// collectComputeTags appends tag records for instances, volumes, snapshots, and
// images owned by the compute driver.
func (h *Handler) collectComputeTags(ctx context.Context, recs []tagRecord) []tagRecord {
	if h.compute == nil {
		return recs
	}

	if insts, err := h.compute.DescribeInstances(ctx, nil, nil); err == nil {
		for i := range insts {
			recs = appendTagRecords(recs, insts[i].ID, "instance", insts[i].Tags)
		}
	}

	if vols, err := h.compute.DescribeVolumes(ctx, nil); err == nil {
		for i := range vols {
			recs = appendTagRecords(recs, vols[i].ID, "volume", vols[i].Tags)
		}
	}

	if snaps, err := h.compute.DescribeSnapshots(ctx, nil); err == nil {
		for _, s := range snaps {
			recs = appendTagRecords(recs, s.ID, "snapshot", s.Tags)
		}
	}

	if imgs, err := h.compute.DescribeImages(ctx, nil); err == nil {
		for _, im := range imgs {
			recs = appendTagRecords(recs, im.ID, "image", im.Tags)
		}
	}

	return recs
}

// collectNetworkTags appends tag records for VPCs, subnets, and security groups
// owned by the networking driver.
func (h *Handler) collectNetworkTags(ctx context.Context, recs []tagRecord) []tagRecord {
	if h.vpc == nil {
		return recs
	}

	if vpcs, err := h.vpc.DescribeVPCs(ctx, nil); err == nil {
		for _, v := range vpcs {
			recs = appendTagRecords(recs, v.ID, "vpc", v.Tags)
		}
	}

	if subnets, err := h.vpc.DescribeSubnets(ctx, nil); err == nil {
		for _, s := range subnets {
			recs = appendTagRecords(recs, s.ID, "subnet", s.Tags)
		}
	}

	if sgs, err := h.vpc.DescribeSecurityGroups(ctx, nil); err == nil {
		for i := range sgs {
			recs = appendTagRecords(recs, sgs[i].ID, "security-group", sgs[i].Tags)
			recs = appendSGRuleTagRecords(recs, sgs[i].IngressRules)
			recs = appendSGRuleTagRecords(recs, sgs[i].EgressRules)
		}
	}

	return recs
}

// appendSGRuleTagRecords appends tag records for each security-group rule that
// carries tags, keyed by the rule's sgr- id.
func appendSGRuleTagRecords(recs []tagRecord, rules []netdriver.SecurityRule) []tagRecord {
	for i := range rules {
		recs = appendTagRecords(recs, rules[i].RuleID, "security-group-rule", rules[i].Tags)
	}

	return recs
}

func appendTagRecords(recs []tagRecord, id, resourceType string, tags map[string]string) []tagRecord {
	for k, v := range tags {
		recs = append(recs, tagRecord{resourceID: id, resourceType: resourceType, key: k, value: v})
	}

	return recs
}

// tagMatchesFilters reports whether a record satisfies every filter (filters
// are ANDed; values within a filter are ORed), matching EC2 DescribeTags.
func tagMatchesFilters(rec tagRecord, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !tagMatchesFilter(rec, f) {
			return false
		}
	}

	return true
}

func tagMatchesFilter(rec tagRecord, f awsquery.Filter) bool {
	var field string

	switch f.Name {
	case "key":
		field = rec.key
	case "resource-id":
		field = rec.resourceID
	case "resource-type":
		field = rec.resourceType
	case "value":
		field = rec.value
	default:
		return true
	}

	for _, v := range f.Values {
		if v == field {
			return true
		}
	}

	return false
}

// createTags applies tags to one or more resources, dispatching each resource
// ID by prefix to the owning provider (VPC-family IDs to the networking
// provider, compute IDs to the compute tagger).
func (h *Handler) createTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	tags := awsquery.FlatTags(r.Form, "Tag")

	if code, msg, ok := validateUserTags(tags); !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, code, msg)
		return
	}

	for _, id := range ids {
		if err := h.tagResource(r.Context(), id, tags); err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(id), "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, tagsResponseXML{Return: true, RequestID: "cloudemu"})
}

// maxUserTagsPerResource is the ceiling EC2 enforces on user tags per resource;
// reservedTagPrefix is the "aws:" namespace reserved for AWS-managed tags that a
// CreateTags call may not write.
const (
	maxUserTagsPerResource = 50
	reservedTagPrefix      = "aws:"
)

// validateUserTags enforces the CreateTags restrictions real EC2 applies before
// any tag is written: at most 50 user tags per resource (TagLimitExceeded), and
// no key in the reserved "aws:" namespace (InvalidTagKey.Malformed). Only the
// key is checked — real EC2 permits a value that starts with "aws:". It returns
// the wire error code and message plus ok=false when a rule is violated.
func validateUserTags(tags map[string]string) (code, msg string, ok bool) {
	if len(tags) > maxUserTagsPerResource {
		return "TagLimitExceeded",
			"The maximum number of tags per resource is 50", false
	}

	for k := range tags {
		if strings.HasPrefix(k, reservedTagPrefix) {
			return "InvalidTagKey.Malformed",
				"The specified tag key is not valid. Tag keys cannot be empty or null, and cannot start with aws:", false
		}
	}

	return "", "", true
}

// deleteTags removes tags (by key) from one or more resources.
func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	tags := awsquery.FlatTags(r.Form, "Tag")

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	for _, id := range ids {
		if err := h.untagResource(r.Context(), id, keys); err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(id), "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, deleteTagsResponseXML{Return: true, RequestID: "cloudemu"})
}

// tagNotFoundCode returns the "…NotFound" error code real EC2 emits when
// CreateTags/DeleteTags names a non-existent resource. An instance id yields the
// resource-specific InvalidInstanceID.NotFound; other resource types fall back
// to the generic InvalidID.NotFound.
func tagNotFoundCode(id string) string {
	switch {
	case strings.HasPrefix(id, "i-"):
		return codeInvalidInstanceID
	case strings.HasPrefix(id, "sgr-"):
		return "InvalidSecurityGroupRuleId.NotFound"
	default:
		return "InvalidID.NotFound"
	}
}

// networkResourceTagPrefixes are the VPC-family id prefixes whose tags the
// networking driver owns through the NetworkResourceTagger optional interface
// (resources without a dedicated Update*Tags method). vpc-/subnet-/sg- keep
// their own methods and are handled separately.
//
//nolint:gochecknoglobals // static id-prefix routing table
var networkResourceTagPrefixes = []string{"rtb-", "igw-", "nat-", "acl-", "dopt-", "pcx-", "pl-", "eigw-", "sgr-"}

// networkTaggableID reports whether id belongs to a resource tagged via the
// NetworkResourceTagger optional interface.
func networkTaggableID(id string) bool {
	for _, p := range networkResourceTagPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}

	return false
}

func (h *Handler) tagResource(ctx context.Context, id string, tags map[string]string) error {
	switch {
	case strings.HasPrefix(id, "vpc-"):
		return h.vpc.UpdateVPCTags(ctx, id, tags)
	case strings.HasPrefix(id, "subnet-"):
		return h.vpc.UpdateSubnetTags(ctx, id, tags)
	case strings.HasPrefix(id, "sg-"):
		return h.vpc.UpdateSecurityGroupTags(ctx, id, tags)
	case networkTaggableID(id):
		if tagger, ok := h.vpc.(netdriver.NetworkResourceTagger); ok {
			return tagger.UpdateResourceTags(ctx, id, tags)
		}

		return nil
	default:
		if tagger, ok := h.compute.(computeTagger); ok {
			return tagger.TagResource(ctx, id, tags)
		}

		return nil
	}
}

func (h *Handler) untagResource(ctx context.Context, id string, keys []string) error {
	switch {
	case strings.HasPrefix(id, "vpc-"):
		return h.vpc.RemoveVPCTags(ctx, id, keys)
	case strings.HasPrefix(id, "subnet-"):
		return h.vpc.RemoveSubnetTags(ctx, id, keys)
	case strings.HasPrefix(id, "sg-"):
		return h.vpc.RemoveSecurityGroupTags(ctx, id, keys)
	case networkTaggableID(id):
		if tagger, ok := h.vpc.(netdriver.NetworkResourceTagger); ok {
			return tagger.RemoveResourceTags(ctx, id, keys)
		}

		return nil
	default:
		if tagger, ok := h.compute.(computeTagger); ok {
			return tagger.UntagResource(ctx, id, keys)
		}

		return nil
	}
}
