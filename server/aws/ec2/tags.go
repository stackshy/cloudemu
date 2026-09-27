package ec2

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

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
	recs = h.collectAddressingTags(r.Context(), recs)

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

// collectAddressingTags appends tag records for Elastic IP allocations, VPC
// endpoints and VPC endpoint services, using the resource-type names real EC2
// DescribeTags reports for them.
func (h *Handler) collectAddressingTags(ctx context.Context, recs []tagRecord) []tagRecord {
	if h.vpc == nil {
		return recs
	}

	if eips, err := h.vpc.DescribeAddresses(ctx, nil); err == nil {
		for i := range eips {
			recs = appendTagRecords(recs, eips[i].AllocationID, "elastic-ip", eips[i].Tags)
		}
	}

	if eps, err := h.vpc.DescribeVPCEndpoints(ctx, nil); err == nil {
		for i := range eps {
			recs = appendTagRecords(recs, eps[i].ID, "vpc-endpoint", eps[i].Tags)
		}
	}

	if svcs, ok := h.vpc.(netdriver.VPCEndpointServices); ok {
		if list, err := svcs.DescribeVPCEndpointServiceConfigurations(ctx, nil); err == nil {
			for i := range list {
				recs = appendTagRecords(recs, list[i].ID, "vpc-endpoint-service", list[i].Tags)
			}
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

// tagReader is the optional read side of the EC2 taggers: the current tags of
// one resource, NotFound when it does not exist. The AWS compute and VPC
// providers implement it; the handler uses it to check a whole CreateTags /
// DeleteTags batch before writing any of it.
type tagReader interface {
	ResourceTags(ctx context.Context, id string) (map[string]string, error)
}

// currentTags returns the tags on id from the provider that owns it. A provider
// that cannot report tags yields a nil map after an existence probe (a no-op
// CreateTags), so the batch is still checked for unknown ids.
func (h *Handler) currentTags(ctx context.Context, id string) (map[string]string, error) {
	var owner any = h.compute
	if networkOwnedTagID(id) {
		owner = h.vpc
	}

	if rd, ok := owner.(tagReader); ok {
		return rd.ResourceTags(ctx, id)
	}

	return nil, h.tagResource(ctx, id, nil)
}

// networkOwnedTagID reports whether the networking provider owns id's tags.
func networkOwnedTagID(id string) bool {
	return strings.HasPrefix(id, "vpc-") || strings.HasPrefix(id, "subnet-") ||
		strings.HasPrefix(id, "sg-") || networkTaggableID(id)
}

// createTags applies tags to one or more resources, dispatching each resource
// ID by prefix to the owning provider (VPC-family IDs to the networking
// provider, compute IDs to the compute tagger). Real EC2 checks the whole batch
// before it writes: an unknown id or a resource that would pass the tag limit
// fails the call with nothing tagged, so every id is checked first.
func (h *Handler) createTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	tags := awsquery.FlatTags(r.Form, "Tag")

	if code, msg, ok := validateUserTags(tags); !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, code, msg)
		return
	}

	for _, id := range ids {
		existing, err := h.currentTags(r.Context(), id)
		if err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(id), "IncorrectState")
			return
		}

		if userTagCountAfter(existing, tags) > maxUserTagsPerResource {
			awsquery.WriteXMLError(w, http.StatusBadRequest, codeTagLimitExceeded, msgTagLimitExceeded)
			return
		}
	}

	for _, id := range ids {
		if err := h.tagResource(r.Context(), id, tags); err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(id), "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, tagsResponseXML{Return: true, RequestID: "cloudemu"})
}

// The CreateTags limits from the EC2 tag restrictions: at most 50 user tags per
// resource, keys up to 128 and values up to 256 Unicode characters, and the
// "aws:" key namespace reserved for AWS-generated tags (which do not count
// toward the 50).
const (
	maxUserTagsPerResource = 50
	maxTagKeyLen           = 128
	maxTagValueLen         = 256
	reservedTagPrefix      = "aws:"

	codeTagLimitExceeded     = "TagLimitExceeded"
	codeTagLengthExceeded    = "InvalidParameterValue"
	codeInvalidVpcEndpointID = "InvalidVpcEndpointId.NotFound"
	msgTagLimitExceeded      = "The maximum number of tags per resource is 50"
)

// userTagCountAfter is how many user (non-"aws:") tags a resource holds once
// tags are merged onto existing: a key already present is overwritten, not
// added.
func userTagCountAfter(existing, tags map[string]string) int {
	n := 0

	for k := range existing {
		if _, overwritten := tags[k]; !overwritten && !strings.HasPrefix(k, reservedTagPrefix) {
			n++
		}
	}

	return n + len(tags)
}

// validateUserTags enforces the per-request CreateTags restrictions real EC2
// applies before any tag is written: no more than 50 tags in the request
// (TagLimitExceeded), no key in the reserved "aws:" namespace
// (InvalidTagKey.Malformed), keys of at most 128 and values of at most 256
// characters (InvalidParameterValue). A value that starts with "aws:" is
// permitted. The per-resource limit, which also counts the tags a resource
// already has, is checked by createTags. It returns the wire error code and
// message plus ok=false when a rule is violated.
func validateUserTags(tags map[string]string) (code, msg string, ok bool) {
	if len(tags) > maxUserTagsPerResource {
		return codeTagLimitExceeded, msgTagLimitExceeded, false
	}

	for k, v := range tags {
		if strings.HasPrefix(k, reservedTagPrefix) {
			return "InvalidTagKey.Malformed",
				"The specified tag key is not valid. Tag keys cannot be empty or null, and cannot start with aws:", false
		}

		if utf8.RuneCountInString(k) > maxTagKeyLen {
			return codeTagLengthExceeded,
				fmt.Sprintf("Tag key exceeds the maximum length of %d characters", maxTagKeyLen), false
		}

		if utf8.RuneCountInString(v) > maxTagValueLen {
			return codeTagLengthExceeded,
				fmt.Sprintf("Tag value exceeds the maximum length of %d characters", maxTagValueLen), false
		}
	}

	return "", "", true
}

// deleteTagSpec is one Tag.N entry of a DeleteTags request. hasValue is false
// when the request sent no Tag.N.Value at all, which deletes the key whatever
// its value; an explicit Value (even "") deletes it only on an exact match.
type deleteTagSpec struct {
	key      string
	value    string
	hasValue bool
}

// parseDeleteTags reads the Tag.N.Key / Tag.N.Value pairs of a DeleteTags
// request, keeping whether each Value was present. Entries without a key are
// skipped, as FlatTags does.
func parseDeleteTags(form url.Values) []deleteTagSpec {
	idxs := awsquery.CollectIndices(form, "Tag")
	specs := make([]deleteTagSpec, 0, len(idxs))

	for _, idx := range idxs {
		base := "Tag." + strconv.Itoa(idx)

		k := form.Get(base + ".Key")
		if k == "" {
			continue
		}

		_, hasValue := form[base+".Value"]
		specs = append(specs, deleteTagSpec{key: k, value: form.Get(base + ".Value"), hasValue: hasValue})
	}

	return specs
}

// deleteTagKeys resolves a DeleteTags request against one resource's current
// tags into the keys to remove. With no Tag entries it is every user tag (EC2
// never deletes "aws:" tags that way); otherwise it is each named key that is
// present and, when the entry carries a value, holds exactly that value.
// write is false when nothing on the resource matches, so the caller skips a
// removal whose empty key list the provider would read as "delete all".
//
// existing is nil when the provider cannot report tags; then the named keys are
// passed through unchecked (and an empty list is the provider's delete-all).
func deleteTagKeys(existing map[string]string, specs []deleteTagSpec) (keys []string, write bool) {
	if existing == nil {
		keys = make([]string, 0, len(specs))
		for _, s := range specs {
			keys = append(keys, s.key)
		}

		return keys, true
	}

	if len(specs) == 0 {
		for k := range existing {
			if !strings.HasPrefix(k, reservedTagPrefix) {
				keys = append(keys, k)
			}
		}

		return keys, len(keys) > 0
	}

	for _, s := range specs {
		v, ok := existing[s.key]
		if !ok || (s.hasValue && v != s.value) {
			continue
		}

		keys = append(keys, s.key)
	}

	return keys, len(keys) > 0
}

// deleteTags removes tags from one or more resources. Like createTags it checks
// every id (and resolves which keys match) before it removes anything, so an
// unknown id fails the batch with nothing deleted.
func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "ResourceId")
	specs := parseDeleteTags(r.Form)

	type removal struct {
		id   string
		keys []string
	}

	plan := make([]removal, 0, len(ids))

	for _, id := range ids {
		existing, err := h.currentTags(r.Context(), id)
		if err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(id), "IncorrectState")
			return
		}

		if keys, write := deleteTagKeys(existing, specs); write {
			plan = append(plan, removal{id: id, keys: keys})
		}
	}

	for _, p := range plan {
		if err := h.untagResource(r.Context(), p.id, p.keys); err != nil {
			writeErrWithNotFound(w, err, tagNotFoundCode(p.id), "IncorrectState")
			return
		}
	}

	awsquery.WriteXMLResponse(w, deleteTagsResponseXML{Return: true, RequestID: "cloudemu"})
}

// tagNotFoundCode returns the "…NotFound" error code real EC2 emits when
// CreateTags/DeleteTags names a non-existent resource: the resource-specific
// code the EC2 error reference defines (and this package already returns from
// the resource's own actions) where there is one, and the generic
// InvalidID.NotFound for the rest. vpce-svc- is matched before vpce-, whose
// prefix it shares.
func tagNotFoundCode(id string) string {
	switch {
	case strings.HasPrefix(id, "i-"):
		return codeInvalidInstanceID
	case strings.HasPrefix(id, "sgr-"):
		return "InvalidSecurityGroupRuleId.NotFound"
	case strings.HasPrefix(id, "eipalloc-"):
		return "InvalidAllocationID.NotFound"
	case strings.HasPrefix(id, "vpce-svc-"):
		return "InvalidVpcEndpointServiceId.NotFound"
	case strings.HasPrefix(id, "vpce-"):
		return codeInvalidVpcEndpointID
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
var networkResourceTagPrefixes = []string{
	"rtb-", "igw-", "nat-", "acl-", "dopt-", "pcx-", "pl-", "eigw-", "sgr-",
	"eipalloc-", "vpce-", // vpce- also covers vpce-svc- endpoint services
}

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
