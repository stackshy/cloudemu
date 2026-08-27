package ec2

import (
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) dhcpOptionSets() (netdriver.DHCPOptionSets, bool) {
	d, ok := h.vpc.(netdriver.DHCPOptionSets)

	return d, ok
}

type dhcpConfigValueXML struct {
	Value string `xml:"value"`
}

type dhcpConfigXML struct {
	Key    string               `xml:"key"`
	Values []dhcpConfigValueXML `xml:"valueSet>item"`
}

type dhcpOptionsXML struct {
	DhcpOptionsID      string          `xml:"dhcpOptionsId"`
	OwnerID            string          `xml:"ownerId"`
	DhcpConfigurations []dhcpConfigXML `xml:"dhcpConfigurationSet>item,omitempty"`
	Tags               []tagItem       `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeDHCPOptions(w http.ResponseWriter, r *http.Request, action string) bool {
	d, ok := h.dhcpOptionSets()
	if !ok {
		return false
	}

	switch action {
	case "CreateDhcpOptions":
		h.createDHCPOptions(w, r, d)
	case "DeleteDhcpOptions":
		h.deleteDHCPOptions(w, r, d)
	case "DescribeDhcpOptions":
		h.describeDHCPOptions(w, r, d)
	case "AssociateDhcpOptions":
		h.associateDHCPOptions(w, r, d)
	default:
		return false
	}

	return true
}

func (h *Handler) createDHCPOptions(w http.ResponseWriter, r *http.Request, d netdriver.DHCPOptionSets) {
	out, err := d.CreateDHCPOptions(r.Context(), netdriver.DHCPOptionsConfig{
		Configuration: parseDHCPConfigurations(r),
		Tags:          mergeTagSpecs(awsquery.TagSpecs(r.Form), "dhcp-options"),
	})
	if err != nil {
		writeDHCPErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name       `xml:"CreateDhcpOptionsResponse"`
		Xmlns   string         `xml:"xmlns,attr"`
		Req     string         `xml:"requestId"`
		Opts    dhcpOptionsXML `xml:"dhcpOptions"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Opts: h.toDHCPOptionsXML(out)})
}

func (*Handler) deleteDHCPOptions(w http.ResponseWriter, r *http.Request, d netdriver.DHCPOptionSets) {
	if err := d.DeleteDHCPOptions(r.Context(), r.Form.Get("DhcpOptionsId")); err != nil {
		writeDHCPErr(w, err)
		return
	}

	writeReturnTrue(w, "DeleteDhcpOptionsResponse")
}

func (h *Handler) describeDHCPOptions(w http.ResponseWriter, r *http.Request, d netdriver.DHCPOptionSets) {
	items, err := d.DescribeDHCPOptions(r.Context(), awsquery.ListStrings(r.Form, "DhcpOptionsId"))
	if err != nil {
		writeDHCPErr(w, err)
		return
	}

	filters := awsquery.Filters(r.Form)
	if err := validateNetworkingFilters(filters, dhcpFilterMatch); err != nil {
		writeDHCPErr(w, err)
		return
	}

	out := filterXML(items, filters, dhcpMatchesFilters, h.toDHCPOptionsXML)

	page, next := pageNetworkingXML(out, r, func(d dhcpOptionsXML) string { return d.DhcpOptionsID })

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"DescribeDhcpOptionsResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Set     []dhcpOptionsXML `xml:"dhcpOptionsSet>item"`
		Next    string           `xml:"nextToken,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: page, Next: next})
}

func (*Handler) associateDHCPOptions(w http.ResponseWriter, r *http.Request, d netdriver.DHCPOptionSets) {
	if err := d.AssociateDHCPOptions(r.Context(), r.Form.Get("DhcpOptionsId"), r.Form.Get("VpcId")); err != nil {
		writeDHCPErr(w, err)
		return
	}

	writeReturnTrue(w, "AssociateDhcpOptionsResponse")
}

// parseDHCPConfigurations reads DhcpConfiguration.N.Key + .Value.M groups.
func parseDHCPConfigurations(r *http.Request) map[string][]string {
	out := map[string][]string{}

	for i := 1; ; i++ {
		key := r.Form.Get("DhcpConfiguration." + strconv.Itoa(i) + ".Key")
		if key == "" {
			break
		}

		out[key] = awsquery.ListStrings(r.Form, "DhcpConfiguration."+strconv.Itoa(i)+".Value")
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func (h *Handler) toDHCPOptionsXML(d *netdriver.DHCPOptions) dhcpOptionsXML {
	keys := make([]string, 0, len(d.Configuration))
	for k := range d.Configuration {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	cfgs := make([]dhcpConfigXML, 0, len(keys))

	for _, k := range keys {
		vals := make([]dhcpConfigValueXML, 0, len(d.Configuration[k]))
		for _, v := range d.Configuration[k] {
			vals = append(vals, dhcpConfigValueXML{Value: v})
		}

		cfgs = append(cfgs, dhcpConfigXML{Key: k, Values: vals})
	}

	return dhcpOptionsXML{DhcpOptionsID: d.ID, OwnerID: h.accountID, DhcpConfigurations: cfgs, Tags: toTagItems(d.Tags)}
}

func dhcpMatchesFilters(d *netdriver.DHCPOptions, filters []awsquery.Filter) bool {
	return matchNetworkingFilters(d, filters, dhcpFilterMatch)
}

// dhcpFilterMatch reports whether d satisfies filter f and whether f is a filter
// DescribeDhcpOptions recognizes. The key/value filters match against the option
// set's configuration entries (e.g. key=domain-name-servers).
func dhcpFilterMatch(d *netdriver.DHCPOptions, f awsquery.Filter) (matched, known bool) {
	switch f.Name {
	case filterDHCPOptionsID:
		return containsString(f.Values, d.ID), true
	case "key":
		return dhcpConfigHasKey(d.Configuration, f.Values), true
	case "value":
		return dhcpConfigHasValue(d.Configuration, f.Values), true
	default:
		return tagFilterMatch(f.Name, f.Values, d.Tags)
	}
}

func dhcpConfigHasKey(cfg map[string][]string, values []string) bool {
	for k := range cfg {
		if containsString(values, k) {
			return true
		}
	}

	return false
}

func dhcpConfigHasValue(cfg map[string][]string, values []string) bool {
	for _, vals := range cfg {
		for _, v := range vals {
			if containsString(values, v) {
				return true
			}
		}
	}

	return false
}

func writeDHCPErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidDhcpOptionID.NotFound", "DependencyViolation")
}
