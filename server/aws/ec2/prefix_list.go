package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) prefixLists() (netdriver.PrefixLists, bool) {
	p, ok := h.vpc.(netdriver.PrefixLists)

	return p, ok
}

type prefixListXML struct {
	PrefixListID   string    `xml:"prefixListId"`
	PrefixListArn  string    `xml:"prefixListArn,omitempty"`
	PrefixListName string    `xml:"prefixListName"`
	AddressFamily  string    `xml:"addressFamily"`
	MaxEntries     *int      `xml:"maxEntries,omitempty"`
	State          string    `xml:"state"`
	Version        *int      `xml:"version,omitempty"`
	OwnerID        string    `xml:"ownerId,omitempty"`
	Tags           []tagItem `xml:"tagSet>item,omitempty"`
}

type prefixListEntryXML struct {
	Cidr        string `xml:"cidr"`
	Description string `xml:"description,omitempty"`
}

func (h *Handler) routePrefixLists(w http.ResponseWriter, r *http.Request, action string) bool {
	p, ok := h.prefixLists()
	if !ok {
		return false
	}

	switch action {
	case "CreateManagedPrefixList":
		h.createPrefixList(w, r, p)
	case "DeleteManagedPrefixList":
		h.deletePrefixList(w, r, p)
	case "DescribeManagedPrefixLists":
		h.describePrefixLists(w, r, p)
	case "GetManagedPrefixListEntries":
		h.getPrefixListEntries(w, r, p)
	case "ModifyManagedPrefixList":
		h.modifyPrefixList(w, r, p)
	default:
		return false
	}

	return true
}

func (h *Handler) createPrefixList(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	maxEntries, _ := strconv.Atoi(r.Form.Get("MaxEntries"))

	out, err := p.CreateManagedPrefixList(r.Context(), netdriver.PrefixListConfig{
		Name:          r.Form.Get("PrefixListName"),
		AddressFamily: r.Form.Get("AddressFamily"),
		MaxEntries:    maxEntries,
		Entries:       parsePrefixListEntries(r),
		Tags:          mergeTagSpecs(awsquery.TagSpecs(r.Form), "prefix-list"),
	})
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:"CreateManagedPrefixListResponse"`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		PL      prefixListXML `xml:"prefixList"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, PL: h.toPrefixListXML(regionFromRequest(r), out)})
}

func (h *Handler) deletePrefixList(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	out, err := p.DeleteManagedPrefixList(r.Context(), r.Form.Get("PrefixListId"))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:"DeleteManagedPrefixListResponse"`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		PL      prefixListXML `xml:"prefixList"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, PL: h.toPrefixListXML(regionFromRequest(r), out)})
}

// describePrefixLists answers DescribeManagedPrefixLists. Like real EC2 it
// returns the AWS-owned service lists (owner AWS) next to the account's own,
// and an explicitly named id that is neither is InvalidPrefixListID.NotFound.
func (h *Handler) describePrefixLists(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, h.matchManagedPrefixListFilter); err != nil {
		writePrefixListErr(w, err)
		return
	}

	ids := awsquery.ListStrings(r.Form, "PrefixListId")

	items, err := p.DescribeManagedPrefixLists(r.Context(), ids)
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	items = append(items, h.awsManagedPrefixLists(r, ids)...)

	if missing := missingPrefixListID(ids, items); missing != "" {
		writePrefixListErr(w, cerrors.Newf(cerrors.NotFound, "The prefix list ID '%s' does not exist", missing))
		return
	}

	region := regionFromRequest(r)

	out := make([]prefixListXML, 0, len(items))

	for i := range items {
		if matchNetworkingFilters(&items[i], filters, h.matchManagedPrefixListFilter) {
			out = append(out, h.toPrefixListXML(region, &items[i]))
		}
	}

	page, next := pageNetworkingXML(out, r, func(x prefixListXML) string { return x.PrefixListID })

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:"DescribeManagedPrefixListsResponse"`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Set     []prefixListXML `xml:"prefixListSet>item"`
		Next    string          `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: page, Next: next})
}

// missingPrefixListID returns the first of ids with no list in items.
func missingPrefixListID(ids []string, items []netdriver.PrefixList) string {
	found := make(map[string]bool, len(items))
	for i := range items {
		found[items[i].ID] = true
	}

	for _, id := range ids {
		if !found[id] {
			return id
		}
	}

	return ""
}

func (h *Handler) getPrefixListEntries(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	id := r.Form.Get("PrefixListId")

	entries, err := p.GetManagedPrefixListEntries(r.Context(), id)
	if cerrors.IsNotFound(err) {
		// The AWS-owned service lists are readable too.
		if owned := h.awsManagedPrefixLists(r, []string{id}); len(owned) == 1 {
			entries, err = owned[0].Entries, nil
		}
	}

	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	out := make([]prefixListEntryXML, 0, len(entries))
	for i := range entries {
		out = append(out, prefixListEntryXML{Cidr: entries[i].CIDR, Description: entries[i].Description})
	}

	// Entries keep their list order; the cidr is unique within a list, so it
	// doubles as the page token key.
	page, next := paginateXML(out, r.Form.Get("MaxResults"), r.Form.Get("NextToken"),
		func(e prefixListEntryXML) string { return e.Cidr })

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name             `xml:"GetManagedPrefixListEntriesResponse"`
		Xmlns   string               `xml:"xmlns,attr"`
		Req     string               `xml:"requestId"`
		Set     []prefixListEntryXML `xml:"entrySet>item"`
		Next    string               `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: page, Next: next})
}

func (h *Handler) modifyPrefixList(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	id := r.Form.Get("PrefixListId")

	if err := checkPrefixListVersion(r, p, id); err != nil {
		writePrefixListErr(w, err)
		return
	}

	out, err := p.ModifyManagedPrefixList(r.Context(),
		id, parseAddPrefixListEntries(r), parseRemovePrefixListCIDRs(r))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name      `xml:"ModifyManagedPrefixListResponse"`
		Xmlns   string        `xml:"xmlns,attr"`
		Req     string        `xml:"requestId"`
		PL      prefixListXML `xml:"prefixList"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, PL: h.toPrefixListXML(regionFromRequest(r), out)})
}

// checkPrefixListVersion enforces the optimistic-concurrency guard AWS applies
// to ModifyManagedPrefixList: when the caller passes CurrentVersion it must
// match the list's current version, else the modify is rejected as an
// IncorrectState. Callers that omit CurrentVersion skip the check.
func checkPrefixListVersion(r *http.Request, p netdriver.PrefixLists, id string) error {
	raw := r.Form.Get("CurrentVersion")
	if raw == "" {
		return nil
	}

	want, err := strconv.Atoi(raw)
	if err != nil {
		return newInvalidParameterErr("CurrentVersion must be an integer")
	}

	lists, err := p.DescribeManagedPrefixLists(r.Context(), []string{id})
	if err != nil {
		return err
	}

	if len(lists) == 0 {
		return cerrors.Newf(cerrors.NotFound, "managed prefix list %q not found", id)
	}

	if lists[0].Version != want {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"prefix list %q has version %d, not the requested %d", id, lists[0].Version, want)
	}

	return nil
}

func parseAddPrefixListEntries(r *http.Request) []netdriver.PrefixListEntry {
	var out []netdriver.PrefixListEntry

	for _, prefix := range []string{"AddEntry", "AddEntries"} {
		for i := 1; ; i++ {
			base := prefix + "." + strconv.Itoa(i)

			cidr := r.Form.Get(base + ".Cidr")
			if cidr == "" {
				break
			}

			out = append(out, netdriver.PrefixListEntry{CIDR: cidr, Description: r.Form.Get(base + ".Description")})
		}

		if len(out) > 0 {
			return out
		}
	}

	return out
}

func parseRemovePrefixListCIDRs(r *http.Request) []string {
	var out []string

	for _, prefix := range []string{"RemoveEntry", "RemoveEntries"} {
		for i := 1; ; i++ {
			base := prefix + "." + strconv.Itoa(i)

			cidr := r.Form.Get(base + ".Cidr")
			if cidr == "" {
				break
			}

			out = append(out, cidr)
		}

		if len(out) > 0 {
			return out
		}
	}

	return out
}

// parsePrefixListEntries reads the AddPrefixListEntry list. The EC2 query
// serialization names the member "Entry" (Entry.N.Cidr); older/alternate SDKs
// may use "Entries", so both prefixes are accepted.
func parsePrefixListEntries(r *http.Request) []netdriver.PrefixListEntry {
	for _, prefix := range []string{"Entry", "Entries"} {
		var out []netdriver.PrefixListEntry

		for i := 1; ; i++ {
			base := prefix + "." + strconv.Itoa(i)

			cidr := r.Form.Get(base + ".Cidr")
			if cidr == "" {
				break
			}

			out = append(out, netdriver.PrefixListEntry{CIDR: cidr, Description: r.Form.Get(base + ".Description")})
		}

		if len(out) > 0 {
			return out
		}
	}

	return nil
}

func (h *Handler) toPrefixListXML(region string, p *netdriver.PrefixList) prefixListXML {
	x := prefixListXML{
		PrefixListID: p.ID, PrefixListName: p.Name, AddressFamily: p.AddressFamily,
		State: p.State, OwnerID: nonEmpty(p.OwnerID, h.accountID), Tags: toTagItems(p.Tags),
	}
	x.PrefixListArn = prefixListARN(region, x.OwnerID, p.ID)

	// AWS-owned lists carry no maxEntries or version; customer lists always do.
	if p.OwnerID == "" {
		maxEntries, version := p.MaxEntries, p.Version
		x.MaxEntries, x.Version = &maxEntries, &version
	}

	return x
}

// prefixListARN builds the managed-prefix-list ARN AWS returns; the SDK and
// Terraform read prefixListArn to reference the list in policies and rules.
// The AWS-owned lists carry "aws" in the account field.
func prefixListARN(region, owner, id string) string {
	if id == "" {
		return ""
	}

	if owner == "AWS" {
		owner = "aws"
	}

	return "arn:aws:ec2:" + region + ":" + owner + ":prefix-list/" + id
}

func writePrefixListErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidPrefixListID.NotFound", "IncorrectState")
}
