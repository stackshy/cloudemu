package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	filterPrefixListID   = "prefix-list-id"
	filterPrefixListName = "prefix-list-name"
)

type servicePrefixListXML struct {
	PrefixListID   string   `xml:"prefixListId"`
	PrefixListName string   `xml:"prefixListName"`
	Cidrs          []string `xml:"cidrSet>item"`
}

func (h *Handler) servicePrefixLists() (netdriver.ServicePrefixLists, bool) {
	p, ok := h.vpc.(netdriver.ServicePrefixLists)

	return p, ok
}

func (h *Handler) routeServicePrefixLists(w http.ResponseWriter, r *http.Request, action string) bool {
	if action != "DescribePrefixLists" {
		return false
	}

	p, ok := h.servicePrefixLists()
	if !ok {
		return false
	}

	h.describeServicePrefixLists(w, r, p)

	return true
}

// describeServicePrefixLists answers DescribePrefixLists: the AWS service
// prefix lists for the caller's region. Terraform's aws_vpc_endpoint read
// looks one up by prefix-list-name to fill prefix_list_id and cidr_blocks.
func (*Handler) describeServicePrefixLists(w http.ResponseWriter, r *http.Request, p netdriver.ServicePrefixLists) {
	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, matchServicePrefixListFilter); err != nil {
		writePrefixListErr(w, err)
		return
	}

	lists, err := p.DescribePrefixLists(r.Context(), regionFromRequest(r), awsquery.ListStrings(r.Form, "PrefixListId"))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	out := make([]servicePrefixListXML, 0, len(lists))

	for i := range lists {
		if matchNetworkingFilters(&lists[i], filters, matchServicePrefixListFilter) {
			out = append(out, servicePrefixListXML{
				PrefixListID: lists[i].ID, PrefixListName: lists[i].Name, Cidrs: lists[i].CIDRs,
			})
		}
	}

	page, next := pageNetworkingXML(out, r, func(x servicePrefixListXML) string { return x.PrefixListID })

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"DescribePrefixListsResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Set     []servicePrefixListXML `xml:"prefixListSet>item"`
		Next    string                 `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: page, Next: next})
}

func matchServicePrefixListFilter(pl *netdriver.ServicePrefixList, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case filterPrefixListID:
		return containsString(f.Values, pl.ID), true
	case filterPrefixListName:
		return containsString(f.Values, pl.Name), true
	default:
		return false, false
	}
}

func (h *Handler) matchManagedPrefixListFilter(pl *netdriver.PrefixList, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case filterPrefixListID:
		return containsString(f.Values, pl.ID), true
	case filterPrefixListName:
		return containsString(f.Values, pl.Name), true
	case filterOwnerID:
		return containsString(f.Values, nonEmpty(pl.OwnerID, h.accountID)), true
	default:
		if matched, isTag := matchStorageTagFilter(pl.Tags, f); isTag {
			return matched, true
		}

		return false, false
	}
}

// awsManagedPrefixLists returns the AWS-owned lists matching ids for the
// caller's region, or nil when the backend has none.
func (h *Handler) awsManagedPrefixLists(r *http.Request, ids []string) []netdriver.PrefixList {
	p, ok := h.servicePrefixLists()
	if !ok {
		return nil
	}

	lists, err := p.DescribeAWSManagedPrefixLists(r.Context(), regionFromRequest(r), ids)
	if err != nil {
		return nil
	}

	return lists
}
