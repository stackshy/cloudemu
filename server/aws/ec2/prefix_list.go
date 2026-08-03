package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) prefixLists() (netdriver.PrefixLists, bool) {
	p, ok := h.vpc.(netdriver.PrefixLists)

	return p, ok
}

type prefixListXML struct {
	PrefixListID   string    `xml:"prefixListId"`
	PrefixListName string    `xml:"prefixListName"`
	AddressFamily  string    `xml:"addressFamily"`
	MaxEntries     int       `xml:"maxEntries"`
	State          string    `xml:"state"`
	Version        int       `xml:"version"`
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
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, PL: toPrefixListXML(out)})
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
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, PL: toPrefixListXML(out)})
}

func (h *Handler) describePrefixLists(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
	items, err := p.DescribeManagedPrefixLists(r.Context(), awsquery.ListStrings(r.Form, "PrefixListId"))
	if err != nil {
		writePrefixListErr(w, err)
		return
	}

	out := make([]prefixListXML, 0, len(items))
	for i := range items {
		out = append(out, toPrefixListXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name        `xml:"DescribeManagedPrefixListsResponse"`
		Xmlns   string          `xml:"xmlns,attr"`
		Req     string          `xml:"requestId"`
		Set     []prefixListXML `xml:"prefixListSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (h *Handler) getPrefixListEntries(w http.ResponseWriter, r *http.Request, p netdriver.PrefixLists) {
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

// parsePrefixListEntries reads Entries.N.Cidr + .Description groups.
func parsePrefixListEntries(r *http.Request) []netdriver.PrefixListEntry {
	var out []netdriver.PrefixListEntry

	for i := 1; ; i++ {
		base := "Entries." + strconv.Itoa(i)

		cidr := r.Form.Get(base + ".Cidr")
		if cidr == "" {
			break
		}

		out = append(out, netdriver.PrefixListEntry{CIDR: cidr, Description: r.Form.Get(base + ".Description")})
	}

	return out
}

func toPrefixListXML(p *netdriver.PrefixList) prefixListXML {
	return prefixListXML{
		PrefixListID: p.ID, PrefixListName: p.Name, AddressFamily: p.AddressFamily,
		MaxEntries: p.MaxEntries, State: p.State, Version: p.Version, Tags: toTagItems(p.Tags),
	}
}

func writePrefixListErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidPrefixListID.NotFound", "IncorrectState")
}
