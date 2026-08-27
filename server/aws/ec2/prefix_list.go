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
	MaxEntries     int       `xml:"maxEntries"`
	State          string    `xml:"state"`
	Version        int       `xml:"version"`
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

func (h *Handler) describePrefixLists(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	items, err := p.DescribeManagedPrefixLists(r.Context(), awsquery.ListStrings(r.Form, "PrefixListId"))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	region := regionFromRequest(r)

	out := make([]prefixListXML, 0, len(items))
	for i := range items {
		out = append(out, h.toPrefixListXML(region, &items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:"DescribeManagedPrefixListsResponse"`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Set     []prefixListXML `xml:"prefixListSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getPrefixListEntries(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	entries, err := p.GetManagedPrefixListEntries(r.Context(), r.Form.Get("PrefixListId"))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	out := make([]prefixListEntryXML, 0, len(entries))
	for i := range entries {
		out = append(out, prefixListEntryXML{Cidr: entries[i].CIDR, Description: entries[i].Description})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name             `xml:"GetManagedPrefixListEntriesResponse"`
		Xmlns   string               `xml:"xmlns,attr"`
		Req     string               `xml:"requestId"`
		Set     []prefixListEntryXML `xml:"entrySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
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
	return prefixListXML{
		PrefixListID: p.ID, PrefixListArn: h.prefixListARN(region, p.ID),
		PrefixListName: p.Name, AddressFamily: p.AddressFamily,
		MaxEntries: p.MaxEntries, State: p.State, Version: p.Version,
		OwnerID: h.accountID, Tags: toTagItems(p.Tags),
	}
}

// prefixListARN builds the managed-prefix-list ARN AWS returns; the SDK and
// Terraform read prefixListArn to reference the list in policies and rules.
func (h *Handler) prefixListARN(region, id string) string {
	if id == "" {
		return ""
	}

	return "arn:aws:ec2:" + region + ":" + h.accountID + ":prefix-list/" + id
}

func writePrefixListErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidPrefixListID.NotFound", "IncorrectState")
}
