package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) ipamResolver() (netdriver.IPAMPrefixListResolver, bool) {
	i, ok := h.vpc.(netdriver.IPAMPrefixListResolver)

	return i, ok
}

func (h *Handler) ipamToken() (netdriver.IPAMExternalToken, bool) {
	i, ok := h.vpc.(netdriver.IPAMExternalToken)

	return i, ok
}

type ipamResolverXML struct {
	IpamPrefixListResolverID  string    `xml:"ipamPrefixListResolverId"`
	IpamPrefixListResolverArn string    `xml:"ipamPrefixListResolverArn"`
	IpamID                    string    `xml:"ipamId,omitempty"`
	IpamArn                   string    `xml:"ipamArn,omitempty"`
	IpamRegion                string    `xml:"ipamRegion,omitempty"`
	OwnerID                   string    `xml:"ownerId,omitempty"`
	AddressFamily             string    `xml:"addressFamily,omitempty"`
	Description               string    `xml:"description,omitempty"`
	State                     string    `xml:"state"`
	LastVersionCreationStatus string    `xml:"lastVersionCreationStatus,omitempty"`
	Tags                      []tagItem `xml:"tagSet>item,omitempty"`
}

type ipamResolverTargetXML struct {
	IpamPrefixListResolverTargetID  string    `xml:"ipamPrefixListResolverTargetId"`
	IpamPrefixListResolverTargetArn string    `xml:"ipamPrefixListResolverTargetArn"`
	IpamPrefixListResolverID        string    `xml:"ipamPrefixListResolverId"`
	OwnerID                         string    `xml:"ownerId,omitempty"`
	PrefixListID                    string    `xml:"prefixListId"`
	PrefixListRegion                string    `xml:"prefixListRegion,omitempty"`
	DesiredVersion                  int       `xml:"desiredVersion,omitempty"`
	LastSyncedVersion               int       `xml:"lastSyncedVersion,omitempty"`
	TrackLatestVersion              bool      `xml:"trackLatestVersion"`
	State                           string    `xml:"state"`
	Tags                            []tagItem `xml:"tagSet>item,omitempty"`
}

type ipamTokenXML struct {
	IpamExternalResourceVerificationTokenID  string    `xml:"ipamExternalResourceVerificationTokenId"`
	IpamExternalResourceVerificationTokenArn string    `xml:"ipamExternalResourceVerificationTokenArn"`
	IpamID                                   string    `xml:"ipamId,omitempty"`
	IpamArn                                  string    `xml:"ipamArn,omitempty"`
	IpamRegion                               string    `xml:"ipamRegion,omitempty"`
	TokenName                                string    `xml:"tokenName,omitempty"`
	TokenValue                               string    `xml:"tokenValue,omitempty"`
	State                                    string    `xml:"state"`
	Status                                   string    `xml:"status,omitempty"`
	Tags                                     []tagItem `xml:"tagSet>item,omitempty"`
}

//nolint:gocyclo // flat action dispatch table
func (h *Handler) routeIPAMResolver(w http.ResponseWriter, r *http.Request, action string) bool {
	if h.routeIPAMToken(w, r, action) {
		return true
	}

	ip, ok := h.ipamResolver()
	if !ok {
		return false
	}

	switch action {
	case "CreateIpamPrefixListResolver":
		h.createIpamResolver(w, r, ip)
	case "DescribeIpamPrefixListResolvers":
		h.describeIpamResolvers(w, r, ip)
	case "ModifyIpamPrefixListResolver":
		h.modifyIpamResolver(w, r, ip)
	case "DeleteIpamPrefixListResolver":
		h.deleteIpamResolver(w, r, ip)
	case "CreateIpamPrefixListResolverTarget":
		h.createIpamResolverTarget(w, r, ip)
	case "DescribeIpamPrefixListResolverTargets":
		h.describeIpamResolverTargets(w, r, ip)
	case "ModifyIpamPrefixListResolverTarget":
		h.modifyIpamResolverTarget(w, r, ip)
	case "DeleteIpamPrefixListResolverTarget":
		h.deleteIpamResolverTarget(w, r, ip)
	case "GetIpamPrefixListResolverRules":
		h.getIpamResolverRules(w, r, ip)
	case "GetIpamPrefixListResolverVersions":
		h.getIpamResolverVersions(w, r, ip)
	case "GetIpamPrefixListResolverVersionEntries":
		h.getIpamResolverVersionEntries(w, r, ip)
	default:
		return false
	}

	return true
}

func (h *Handler) routeIPAMToken(w http.ResponseWriter, r *http.Request, action string) bool {
	ip, ok := h.ipamToken()
	if !ok {
		return false
	}

	switch action {
	case "CreateIpamExternalResourceVerificationToken":
		h.createIpamToken(w, r, ip)
	case "DeleteIpamExternalResourceVerificationToken":
		h.deleteIpamToken(w, r, ip)
	case "DescribeIpamExternalResourceVerificationTokens":
		h.describeIpamTokens(w, r, ip)
	default:
		return false
	}

	return true
}

func (*Handler) createIpamResolver(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.CreateIpamPrefixListResolver(r.Context(),
		r.Form.Get("IpamId"), r.Form.Get("AddressFamily"), r.Form.Get("Description"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-prefix-list-resolver"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolver(w, "CreateIpamPrefixListResolverResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamResolvers(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	items, err := ip.DescribeIpamPrefixListResolvers(r.Context(), awsquery.ListStrings(r.Form, "IpamPrefixListResolverId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamResolverXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamResolverXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name          `xml:"DescribeIpamPrefixListResolversResponse"`
		Xmlns   string            `xml:"xmlns,attr"`
		Req     string            `xml:"requestId"`
		Set     []ipamResolverXML `xml:"ipamPrefixListResolverSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamResolver(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.ModifyIpamPrefixListResolver(r.Context(), r.Form.Get("IpamPrefixListResolverId"), r.Form.Get("Description"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolver(w, "ModifyIpamPrefixListResolverResponse", out)
}

func (*Handler) deleteIpamResolver(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.DeleteIpamPrefixListResolver(r.Context(), r.Form.Get("IpamPrefixListResolverId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolver(w, "DeleteIpamPrefixListResolverResponse", out)
}

func writeIpamResolver(w http.ResponseWriter, root string, out *netdriver.IpamPrefixListResolver) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName  xml.Name        `xml:""`
		Xmlns    string          `xml:"xmlns,attr"`
		Req      string          `xml:"requestId"`
		Resolver ipamResolverXML `xml:"ipamPrefixListResolver"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Resolver: toIpamResolverXML(out)})
}

func toIpamResolverXML(r *netdriver.IpamPrefixListResolver) ipamResolverXML {
	return ipamResolverXML{
		IpamPrefixListResolverID: r.ID, IpamPrefixListResolverArn: r.ARN, IpamID: r.IpamID, IpamArn: r.IpamARN,
		IpamRegion: r.IpamRegion, OwnerID: r.OwnerID, AddressFamily: r.AddressFamily, Description: r.Description,
		State: r.State, LastVersionCreationStatus: r.LastVersionCreationStatus, Tags: toTagItems(r.Tags),
	}
}

func (*Handler) createIpamResolverTarget(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.CreateIpamPrefixListResolverTarget(r.Context(),
		r.Form.Get("IpamPrefixListResolverId"), r.Form.Get("PrefixListId"), r.Form.Get("PrefixListRegion"),
		atoiDefault(r.Form.Get("DesiredVersion")), r.Form.Get("TrackLatestVersion") == formTrue,
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-prefix-list-resolver-target"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolverTarget(w, "CreateIpamPrefixListResolverTargetResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamResolverTargets(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	items, err := ip.DescribeIpamPrefixListResolverTargets(r.Context(), awsquery.ListStrings(r.Form, "IpamPrefixListResolverTargetId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamResolverTargetXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamResolverTargetXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                `xml:"DescribeIpamPrefixListResolverTargetsResponse"`
		Xmlns   string                  `xml:"xmlns,attr"`
		Req     string                  `xml:"requestId"`
		Set     []ipamResolverTargetXML `xml:"ipamPrefixListResolverTargetSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) modifyIpamResolverTarget(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.ModifyIpamPrefixListResolverTarget(r.Context(),
		r.Form.Get("IpamPrefixListResolverTargetId"), atoiDefault(r.Form.Get("DesiredVersion")),
		r.Form.Get("TrackLatestVersion") == formTrue)
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolverTarget(w, "ModifyIpamPrefixListResolverTargetResponse", out)
}

func (*Handler) deleteIpamResolverTarget(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	out, err := ip.DeleteIpamPrefixListResolverTarget(r.Context(), r.Form.Get("IpamPrefixListResolverTargetId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamResolverTarget(w, "DeleteIpamPrefixListResolverTargetResponse", out)
}

func writeIpamResolverTarget(w http.ResponseWriter, root string, out *netdriver.IpamPrefixListResolverTarget) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name              `xml:""`
		Xmlns   string                `xml:"xmlns,attr"`
		Req     string                `xml:"requestId"`
		Target  ipamResolverTargetXML `xml:"ipamPrefixListResolverTarget"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Target: toIpamResolverTargetXML(out)})
}

func toIpamResolverTargetXML(t *netdriver.IpamPrefixListResolverTarget) ipamResolverTargetXML {
	return ipamResolverTargetXML{
		IpamPrefixListResolverTargetID: t.ID, IpamPrefixListResolverTargetArn: t.ARN, IpamPrefixListResolverID: t.ResolverID,
		OwnerID: t.OwnerID, PrefixListID: t.PrefixListID, PrefixListRegion: t.PrefixListRegion,
		DesiredVersion: t.DesiredVersion, LastSyncedVersion: t.LastSyncedVersion, TrackLatestVersion: t.TrackLatestVersion,
		State: t.State, Tags: toTagItems(t.Tags),
	}
}

func (*Handler) getIpamResolverRules(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	items, err := ip.GetIpamPrefixListResolverRules(r.Context(), r.Form.Get("IpamPrefixListResolverId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type ruleXML struct {
		IpamPoolID string `xml:"ipamPoolId,omitempty"`
		Cidr       string `xml:"cidr,omitempty"`
	}

	out := make([]ruleXML, 0, len(items))
	for i := range items {
		out = append(out, ruleXML{IpamPoolID: items[i].IpamPoolID, Cidr: items[i].Cidr})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name  `xml:"GetIpamPrefixListResolverRulesResponse"`
		Xmlns   string    `xml:"xmlns,attr"`
		Req     string    `xml:"requestId"`
		Set     []ruleXML `xml:"ruleSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getIpamResolverVersions(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	items, err := ip.GetIpamPrefixListResolverVersions(r.Context(), r.Form.Get("IpamPrefixListResolverId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type versionXML struct {
		Version int `xml:"version"`
	}

	out := make([]versionXML, 0, len(items))
	for i := range items {
		out = append(out, versionXML{Version: items[i].Version})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name     `xml:"GetIpamPrefixListResolverVersionsResponse"`
		Xmlns   string       `xml:"xmlns,attr"`
		Req     string       `xml:"requestId"`
		Set     []versionXML `xml:"ipamPrefixListResolverVersionSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getIpamResolverVersionEntries(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMPrefixListResolver) {
	entries, err := ip.GetIpamPrefixListResolverVersionEntries(r.Context(),
		r.Form.Get("IpamPrefixListResolverId"), atoiDefault(r.Form.Get("Version")))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	type entryXML struct {
		Cidr        string `xml:"cidr"`
		Description string `xml:"description,omitempty"`
	}

	out := make([]entryXML, 0, len(entries))
	for i := range entries {
		out = append(out, entryXML{Cidr: entries[i].CIDR, Description: entries[i].Description})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name   `xml:"GetIpamPrefixListResolverVersionEntriesResponse"`
		Xmlns   string     `xml:"xmlns,attr"`
		Req     string     `xml:"requestId"`
		Set     []entryXML `xml:"entrySet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) createIpamToken(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMExternalToken) {
	out, err := ip.CreateIpamExternalResourceVerificationToken(r.Context(),
		r.Form.Get("IpamId"), r.Form.Get("TokenName"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "ipam-external-resource-verification-token"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamToken(w, "CreateIpamExternalResourceVerificationTokenResponse", out)
}

func (*Handler) deleteIpamToken(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMExternalToken) {
	out, err := ip.DeleteIpamExternalResourceVerificationToken(r.Context(), r.Form.Get("IpamExternalResourceVerificationTokenId"))
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	writeIpamToken(w, "DeleteIpamExternalResourceVerificationTokenResponse", out)
}

//nolint:dupl // parallel per-resource marshaling
func (*Handler) describeIpamTokens(w http.ResponseWriter, r *http.Request, ip netdriver.IPAMExternalToken) {
	items, err := ip.DescribeIpamExternalResourceVerificationTokens(
		r.Context(), awsquery.ListStrings(r.Form, "IpamExternalResourceVerificationTokenId"),
	)
	if err != nil {
		writeIPAMErr(w, err)
		return
	}

	out := make([]ipamTokenXML, 0, len(items))
	for i := range items {
		out = append(out, toIpamTokenXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name       `xml:"DescribeIpamExternalResourceVerificationTokensResponse"`
		Xmlns   string         `xml:"xmlns,attr"`
		Req     string         `xml:"requestId"`
		Set     []ipamTokenXML `xml:"ipamExternalResourceVerificationTokenSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func writeIpamToken(w http.ResponseWriter, root string, out *netdriver.IpamExternalResourceVerificationToken) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name     `xml:""`
		Xmlns   string       `xml:"xmlns,attr"`
		Req     string       `xml:"requestId"`
		Token   ipamTokenXML `xml:"ipamExternalResourceVerificationToken"`
	}{XMLName: xml.Name{Local: root}, Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Token: toIpamTokenXML(out)})
}

func toIpamTokenXML(t *netdriver.IpamExternalResourceVerificationToken) ipamTokenXML {
	return ipamTokenXML{
		IpamExternalResourceVerificationTokenID: t.ID, IpamExternalResourceVerificationTokenArn: t.ARN,
		IpamID: t.IpamID, IpamArn: t.IpamARN, IpamRegion: t.IpamRegion, TokenName: t.TokenName,
		TokenValue: t.TokenValue, State: t.State, Status: t.Status, Tags: toTagItems(t.Tags),
	}
}
