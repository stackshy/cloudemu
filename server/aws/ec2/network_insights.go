package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) networkInsights() (netdriver.NetworkInsights, bool) {
	n, ok := h.vpc.(netdriver.NetworkInsights)

	return n, ok
}

func (h *Handler) routeNetworkInsights(w http.ResponseWriter, r *http.Request, action string) bool {
	n, ok := h.networkInsights()
	if !ok {
		return false
	}

	if h.routeNetworkInsightsPaths(w, r, action, n) {
		return true
	}

	return h.routeNetworkInsightsAccessScopes(w, r, action, n)
}

func (h *Handler) routeNetworkInsightsPaths(
	w http.ResponseWriter, r *http.Request, action string, n netdriver.NetworkInsights,
) bool {
	switch action {
	case "CreateNetworkInsightsPath":
		h.createNetworkInsightsPath(w, r, n)
	case "DeleteNetworkInsightsPath":
		h.deleteNetworkInsightsPath(w, r, n)
	case "DescribeNetworkInsightsPaths":
		h.describeNetworkInsightsPaths(w, r, n)
	case "StartNetworkInsightsAnalysis":
		h.startNetworkInsightsAnalysis(w, r, n)
	case "DeleteNetworkInsightsAnalysis":
		h.deleteNetworkInsightsAnalysis(w, r, n)
	case "DescribeNetworkInsightsAnalyses":
		h.describeNetworkInsightsAnalyses(w, r, n)
	default:
		return false
	}

	return true
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (h *Handler) routeNetworkInsightsAccessScopes(
	w http.ResponseWriter, r *http.Request, action string, n netdriver.NetworkInsights,
) bool {
	switch action {
	case "CreateNetworkInsightsAccessScope":
		h.createNetworkInsightsAccessScope(w, r, n)
	case "DeleteNetworkInsightsAccessScope":
		h.deleteNetworkInsightsAccessScope(w, r, n)
	case "DescribeNetworkInsightsAccessScopes":
		h.describeNetworkInsightsAccessScopes(w, r, n)
	case "GetNetworkInsightsAccessScopeContent":
		h.getNetworkInsightsAccessScopeContent(w, r, n)
	case "StartNetworkInsightsAccessScopeAnalysis":
		h.startNetworkInsightsAccessScopeAnalysis(w, r, n)
	case "DeleteNetworkInsightsAccessScopeAnalysis":
		h.deleteNetworkInsightsAccessScopeAnalysis(w, r, n)
	case "DescribeNetworkInsightsAccessScopeAnalyses":
		h.describeNetworkInsightsAccessScopeAnalyses(w, r, n)
	case "GetNetworkInsightsAccessScopeAnalysisFindings":
		h.getNetworkInsightsAccessScopeAnalysisFindings(w, r, n)
	default:
		return false
	}

	return true
}

// ---- XML shapes ----

type networkInsightsPathXML struct {
	NetworkInsightsPathID  string    `xml:"networkInsightsPathId"`
	NetworkInsightsPathArn string    `xml:"networkInsightsPathArn,omitempty"`
	Protocol               string    `xml:"protocol,omitempty"`
	Source                 string    `xml:"source,omitempty"`
	Destination            string    `xml:"destination,omitempty"`
	SourceIP               string    `xml:"sourceIp,omitempty"`
	DestinationIP          string    `xml:"destinationIp,omitempty"`
	DestinationPort        int32     `xml:"destinationPort,omitempty"`
	CreatedDate            string    `xml:"createdDate,omitempty"`
	Tags                   []tagItem `xml:"tagSet>item,omitempty"`
}

type networkInsightsAnalysisXML struct {
	NetworkInsightsAnalysisID  string    `xml:"networkInsightsAnalysisId"`
	NetworkInsightsAnalysisArn string    `xml:"networkInsightsAnalysisArn,omitempty"`
	NetworkInsightsPathID      string    `xml:"networkInsightsPathId,omitempty"`
	StartDate                  string    `xml:"startDate,omitempty"`
	Status                     string    `xml:"status,omitempty"`
	StatusMessage              string    `xml:"statusMessage,omitempty"`
	NetworkPathFound           bool      `xml:"networkPathFound"`
	FilterInArns               []string  `xml:"filterInArnSet>item,omitempty"`
	FilterOutArns              []string  `xml:"filterOutArnSet>item,omitempty"`
	AdditionalAccounts         []string  `xml:"additionalAccountSet>item,omitempty"`
	Tags                       []tagItem `xml:"tagSet>item,omitempty"`
}

type accessScopeResourceStatementXML struct {
	ResourceTypes []string `xml:"resourceTypeSet>item,omitempty"`
	Resources     []string `xml:"resourceSet>item,omitempty"`
}

type accessScopeStatementXML struct {
	ResourceStatement *accessScopeResourceStatementXML `xml:"resourceStatement,omitempty"`
}

type accessScopePathXML struct {
	Source      *accessScopeStatementXML `xml:"source,omitempty"`
	Destination *accessScopeStatementXML `xml:"destination,omitempty"`
}

type networkInsightsAccessScopeXML struct {
	NetworkInsightsAccessScopeID  string    `xml:"networkInsightsAccessScopeId"`
	NetworkInsightsAccessScopeArn string    `xml:"networkInsightsAccessScopeArn,omitempty"`
	CreatedDate                   string    `xml:"createdDate,omitempty"`
	UpdatedDate                   string    `xml:"updatedDate,omitempty"`
	Tags                          []tagItem `xml:"tagSet>item,omitempty"`
}

type networkInsightsAccessScopeContentXML struct {
	NetworkInsightsAccessScopeID string               `xml:"networkInsightsAccessScopeId"`
	MatchPaths                   []accessScopePathXML `xml:"matchPathSet>item,omitempty"`
	ExcludePaths                 []accessScopePathXML `xml:"excludePathSet>item,omitempty"`
}

type networkInsightsAccessScopeAnalysisXML struct {
	NetworkInsightsAccessScopeAnalysisID  string    `xml:"networkInsightsAccessScopeAnalysisId"`
	NetworkInsightsAccessScopeAnalysisArn string    `xml:"networkInsightsAccessScopeAnalysisArn,omitempty"`
	NetworkInsightsAccessScopeID          string    `xml:"networkInsightsAccessScopeId,omitempty"`
	Status                                string    `xml:"status,omitempty"`
	StatusMessage                         string    `xml:"statusMessage,omitempty"`
	StartDate                             string    `xml:"startDate,omitempty"`
	EndDate                               string    `xml:"endDate,omitempty"`
	FindingsFound                         string    `xml:"findingsFound,omitempty"`
	AnalyzedEniCount                      int32     `xml:"analyzedEniCount,omitempty"`
	Tags                                  []tagItem `xml:"tagSet>item,omitempty"`
}

type accessScopeAnalysisFindingXML struct {
	FindingID                            string `xml:"findingId,omitempty"`
	NetworkInsightsAccessScopeAnalysisID string `xml:"networkInsightsAccessScopeAnalysisId,omitempty"`
	NetworkInsightsAccessScopeID         string `xml:"networkInsightsAccessScopeId,omitempty"`
}

// ---- path handlers ----

func (*Handler) createNetworkInsightsPath(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	out, err := n.CreateNetworkInsightsPath(r.Context(), netdriver.NetworkInsightsPathConfig{
		Protocol:        r.Form.Get("Protocol"),
		Source:          r.Form.Get("Source"),
		Destination:     r.Form.Get("Destination"),
		SourceIP:        r.Form.Get("SourceIp"),
		DestinationIP:   r.Form.Get("DestinationIp"),
		DestinationPort: formInt32(r, "DestinationPort"),
		Tags:            mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-insights-path"),
	})
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsPathId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name               `xml:"CreateNetworkInsightsPathResponse"`
		Xmlns   string                 `xml:"xmlns,attr"`
		Req     string                 `xml:"requestId"`
		Path    networkInsightsPathXML `xml:"networkInsightsPath"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Path: toPathXML(out)})
}

func (*Handler) deleteNetworkInsightsPath(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	id := r.Form.Get("NetworkInsightsPathId")
	if err := n.DeleteNetworkInsightsPath(r.Context(), id); err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsPathId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteNetworkInsightsPathResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"networkInsightsPathId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeNetworkInsightsPaths(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	items, err := n.DescribeNetworkInsightsPaths(r.Context(), awsquery.ListStrings(r.Form, "NetworkInsightsPathId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsPathId.NotFound")
		return
	}

	out := make([]networkInsightsPathXML, 0, len(items))
	for i := range items {
		out = append(out, toPathXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                 `xml:"DescribeNetworkInsightsPathsResponse"`
		Xmlns   string                   `xml:"xmlns,attr"`
		Req     string                   `xml:"requestId"`
		Set     []networkInsightsPathXML `xml:"networkInsightsPathSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) startNetworkInsightsAnalysis(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	out, err := n.StartNetworkInsightsAnalysis(r.Context(), netdriver.NetworkInsightsAnalysisConfig{
		PathID:             r.Form.Get("NetworkInsightsPathId"),
		FilterInARNs:       awsquery.ListStrings(r.Form, "FilterInArn"),
		FilterOutARNs:      awsquery.ListStrings(r.Form, "FilterOutArn"),
		AdditionalAccounts: awsquery.ListStrings(r.Form, "AdditionalAccount"),
		Tags:               mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-insights-analysis"),
	})
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsPathId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName  xml.Name                   `xml:"StartNetworkInsightsAnalysisResponse"`
		Xmlns    string                     `xml:"xmlns,attr"`
		Req      string                     `xml:"requestId"`
		Analysis networkInsightsAnalysisXML `xml:"networkInsightsAnalysis"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Analysis: toAnalysisXML(out)})
}

func (*Handler) deleteNetworkInsightsAnalysis(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	id := r.Form.Get("NetworkInsightsAnalysisId")
	if err := n.DeleteNetworkInsightsAnalysis(r.Context(), id); err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAnalysisId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteNetworkInsightsAnalysisResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"networkInsightsAnalysisId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeNetworkInsightsAnalyses(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	items, err := n.DescribeNetworkInsightsAnalyses(r.Context(),
		awsquery.ListStrings(r.Form, "NetworkInsightsAnalysisId"),
		r.Form.Get("NetworkInsightsPathId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAnalysisId.NotFound")
		return
	}

	out := make([]networkInsightsAnalysisXML, 0, len(items))
	for i := range items {
		out = append(out, toAnalysisXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                     `xml:"DescribeNetworkInsightsAnalysesResponse"`
		Xmlns   string                       `xml:"xmlns,attr"`
		Req     string                       `xml:"requestId"`
		Set     []networkInsightsAnalysisXML `xml:"networkInsightsAnalysisSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

// ---- access-scope handlers ----

func (*Handler) createNetworkInsightsAccessScope(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	out, err := n.CreateNetworkInsightsAccessScope(r.Context(), netdriver.NetworkInsightsAccessScopeConfig{
		MatchPaths:   parseAccessScopePaths(r, "MatchPath"),
		ExcludePaths: parseAccessScopePaths(r, "ExcludePath"),
		Tags:         mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-insights-access-scope"),
	})
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                             `xml:"CreateNetworkInsightsAccessScopeResponse"`
		Xmlns   string                               `xml:"xmlns,attr"`
		Req     string                               `xml:"requestId"`
		Scope   networkInsightsAccessScopeXML        `xml:"networkInsightsAccessScope"`
		Content networkInsightsAccessScopeContentXML `xml:"networkInsightsAccessScopeContent"`
	}{
		Xmlns: awsquery.Namespace, Req: awsquery.RequestID,
		Scope: toAccessScopeXML(out), Content: toAccessScopeContentXML(out),
	})
}

func (*Handler) deleteNetworkInsightsAccessScope(w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights) {
	id := r.Form.Get("NetworkInsightsAccessScopeId")
	if err := n.DeleteNetworkInsightsAccessScope(r.Context(), id); err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteNetworkInsightsAccessScopeResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"networkInsightsAccessScopeId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeNetworkInsightsAccessScopes(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	items, err := n.DescribeNetworkInsightsAccessScopes(r.Context(),
		awsquery.ListStrings(r.Form, "NetworkInsightsAccessScopeId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeId.NotFound")
		return
	}

	out := make([]networkInsightsAccessScopeXML, 0, len(items))
	for i := range items {
		out = append(out, toAccessScopeXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                        `xml:"DescribeNetworkInsightsAccessScopesResponse"`
		Xmlns   string                          `xml:"xmlns,attr"`
		Req     string                          `xml:"requestId"`
		Set     []networkInsightsAccessScopeXML `xml:"networkInsightsAccessScopeSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getNetworkInsightsAccessScopeContent(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	out, err := n.GetNetworkInsightsAccessScopeContent(r.Context(), r.Form.Get("NetworkInsightsAccessScopeId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                             `xml:"GetNetworkInsightsAccessScopeContentResponse"`
		Xmlns   string                               `xml:"xmlns,attr"`
		Req     string                               `xml:"requestId"`
		Content networkInsightsAccessScopeContentXML `xml:"networkInsightsAccessScopeContent"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Content: toAccessScopeContentXML(out)})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) startNetworkInsightsAccessScopeAnalysis(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	out, err := n.StartNetworkInsightsAccessScopeAnalysis(r.Context(),
		r.Form.Get("NetworkInsightsAccessScopeId"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-insights-access-scope-analysis"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName  xml.Name                              `xml:"StartNetworkInsightsAccessScopeAnalysisResponse"`
		Xmlns    string                                `xml:"xmlns,attr"`
		Req      string                                `xml:"requestId"`
		Analysis networkInsightsAccessScopeAnalysisXML `xml:"networkInsightsAccessScopeAnalysis"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Analysis: toAccessScopeAnalysisXML(out)})
}

func (*Handler) deleteNetworkInsightsAccessScopeAnalysis(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	id := r.Form.Get("NetworkInsightsAccessScopeAnalysisId")
	if err := n.DeleteNetworkInsightsAccessScopeAnalysis(r.Context(), id); err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeAnalysisId.NotFound")
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteNetworkInsightsAccessScopeAnalysisResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		ID      string   `xml:"networkInsightsAccessScopeAnalysisId"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, ID: id})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeNetworkInsightsAccessScopeAnalyses(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	items, err := n.DescribeNetworkInsightsAccessScopeAnalyses(r.Context(),
		awsquery.ListStrings(r.Form, "NetworkInsightsAccessScopeAnalysisId"),
		r.Form.Get("NetworkInsightsAccessScopeId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeAnalysisId.NotFound")
		return
	}

	out := make([]networkInsightsAccessScopeAnalysisXML, 0, len(items))
	for i := range items {
		out = append(out, toAccessScopeAnalysisXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name                                `xml:"DescribeNetworkInsightsAccessScopeAnalysesResponse"`
		Xmlns   string                                  `xml:"xmlns,attr"`
		Req     string                                  `xml:"requestId"`
		Set     []networkInsightsAccessScopeAnalysisXML `xml:"networkInsightsAccessScopeAnalysisSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func (*Handler) getNetworkInsightsAccessScopeAnalysisFindings(
	w http.ResponseWriter, r *http.Request, n netdriver.NetworkInsights,
) {
	findings, status, err := n.GetNetworkInsightsAccessScopeAnalysisFindings(r.Context(),
		r.Form.Get("NetworkInsightsAccessScopeAnalysisId"))
	if err != nil {
		writeNetworkInsightsErr(w, err, "InvalidNetworkInsightsAccessScopeAnalysisId.NotFound")
		return
	}

	out := make([]accessScopeAnalysisFindingXML, 0, len(findings))
	for i := range findings {
		out = append(out, accessScopeAnalysisFindingXML{
			FindingID:                            findings[i].FindingID,
			NetworkInsightsAccessScopeAnalysisID: findings[i].AnalysisID,
			NetworkInsightsAccessScopeID:         findings[i].AccessScopeID,
		})
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName    xml.Name                        `xml:"GetNetworkInsightsAccessScopeAnalysisFindingsResponse"`
		Xmlns      string                          `xml:"xmlns,attr"`
		Req        string                          `xml:"requestId"`
		AnalysisID string                          `xml:"networkInsightsAccessScopeAnalysisId,omitempty"`
		Status     string                          `xml:"analysisStatus,omitempty"`
		Findings   []accessScopeAnalysisFindingXML `xml:"analysisFindingSet>item,omitempty"`
	}{
		Xmlns: awsquery.Namespace, Req: awsquery.RequestID,
		AnalysisID: r.Form.Get("NetworkInsightsAccessScopeAnalysisId"), Status: status, Findings: out,
	})
}

// ---- request parsing ----

// parseAccessScopePaths reads MatchPath.N / ExcludePath.N groups, each carrying
// Source/Destination ResourceStatement resource-type and resource lists.
func parseAccessScopePaths(r *http.Request, prefix string) []netdriver.AccessScopePath {
	indices := awsquery.CollectIndices(r.Form, prefix)
	if len(indices) == 0 {
		return nil
	}

	out := make([]netdriver.AccessScopePath, 0, len(indices))

	for _, idx := range indices {
		base := prefix + "." + strconv.Itoa(idx)
		out = append(out, netdriver.AccessScopePath{
			Source:      parseAccessScopeStatement(r, base+".Source"),
			Destination: parseAccessScopeStatement(r, base+".Destination"),
		})
	}

	return out
}

func parseAccessScopeStatement(r *http.Request, base string) *netdriver.AccessScopeStatement {
	types := awsquery.ListStrings(r.Form, base+".ResourceStatement.ResourceType")
	resources := awsquery.ListStrings(r.Form, base+".ResourceStatement.Resource")

	if len(types) == 0 && len(resources) == 0 {
		return nil
	}

	return &netdriver.AccessScopeStatement{
		ResourceStatement: &netdriver.AccessScopeResourceStatement{
			ResourceTypes: types,
			Resources:     resources,
		},
	}
}

// ---- driver → XML ----

func toPathXML(p *netdriver.NetworkInsightsPath) networkInsightsPathXML {
	return networkInsightsPathXML{
		NetworkInsightsPathID:  p.ID,
		NetworkInsightsPathArn: p.ARN,
		Protocol:               p.Protocol,
		Source:                 p.Source,
		Destination:            p.Destination,
		SourceIP:               p.SourceIP,
		DestinationIP:          p.DestinationIP,
		DestinationPort:        p.DestinationPort,
		CreatedDate:            formatTime(p.CreatedDate),
		Tags:                   toTagItems(p.Tags),
	}
}

func toAnalysisXML(a *netdriver.NetworkInsightsAnalysis) networkInsightsAnalysisXML {
	return networkInsightsAnalysisXML{
		NetworkInsightsAnalysisID:  a.ID,
		NetworkInsightsAnalysisArn: a.ARN,
		NetworkInsightsPathID:      a.PathID,
		StartDate:                  formatTime(a.StartDate),
		Status:                     a.Status,
		StatusMessage:              a.StatusMessage,
		NetworkPathFound:           a.NetworkPathFound,
		FilterInArns:               a.FilterInARNs,
		FilterOutArns:              a.FilterOutARNs,
		AdditionalAccounts:         a.AdditionalAccounts,
		Tags:                       toTagItems(a.Tags),
	}
}

func toAccessScopeXML(s *netdriver.NetworkInsightsAccessScope) networkInsightsAccessScopeXML {
	return networkInsightsAccessScopeXML{
		NetworkInsightsAccessScopeID:  s.ID,
		NetworkInsightsAccessScopeArn: s.ARN,
		CreatedDate:                   formatTime(s.CreatedDate),
		UpdatedDate:                   formatTime(s.UpdatedDate),
		Tags:                          toTagItems(s.Tags),
	}
}

func toAccessScopeContentXML(s *netdriver.NetworkInsightsAccessScope) networkInsightsAccessScopeContentXML {
	return networkInsightsAccessScopeContentXML{
		NetworkInsightsAccessScopeID: s.ID,
		MatchPaths:                   toAccessScopePathXMLs(s.MatchPaths),
		ExcludePaths:                 toAccessScopePathXMLs(s.ExcludePaths),
	}
}

func toAccessScopePathXMLs(paths []netdriver.AccessScopePath) []accessScopePathXML {
	if len(paths) == 0 {
		return nil
	}

	out := make([]accessScopePathXML, 0, len(paths))
	for i := range paths {
		out = append(out, accessScopePathXML{
			Source:      toAccessScopeStatementXML(paths[i].Source),
			Destination: toAccessScopeStatementXML(paths[i].Destination),
		})
	}

	return out
}

func toAccessScopeStatementXML(s *netdriver.AccessScopeStatement) *accessScopeStatementXML {
	if s == nil || s.ResourceStatement == nil {
		return nil
	}

	return &accessScopeStatementXML{
		ResourceStatement: &accessScopeResourceStatementXML{
			ResourceTypes: s.ResourceStatement.ResourceTypes,
			Resources:     s.ResourceStatement.Resources,
		},
	}
}

func toAccessScopeAnalysisXML(a *netdriver.NetworkInsightsAccessScopeAnalysis) networkInsightsAccessScopeAnalysisXML {
	return networkInsightsAccessScopeAnalysisXML{
		NetworkInsightsAccessScopeAnalysisID:  a.ID,
		NetworkInsightsAccessScopeAnalysisArn: a.ARN,
		NetworkInsightsAccessScopeID:          a.AccessScopeID,
		Status:                                a.Status,
		StatusMessage:                         a.StatusMessage,
		StartDate:                             formatTime(a.StartDate),
		EndDate:                               formatTime(a.EndDate),
		FindingsFound:                         a.FindingsFound,
		AnalyzedEniCount:                      a.AnalyzedEniCount,
		Tags:                                  toTagItems(a.Tags),
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.Format(time.RFC3339)
}

func writeNetworkInsightsErr(w http.ResponseWriter, err error, notFoundCode string) {
	writeErrWithNotFound(w, err, notFoundCode, "DependencyViolation")
}
