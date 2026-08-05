package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) vpcBlockPublicAccess() (netdriver.VPCBlockPublicAccess, bool) {
	v, ok := h.vpc.(netdriver.VPCBlockPublicAccess)

	return v, ok
}

func (h *Handler) routeVPCBlockPublicAccess(w http.ResponseWriter, r *http.Request, action string) bool {
	v, ok := h.vpcBlockPublicAccess()
	if !ok {
		return false
	}

	switch action {
	case "DescribeVpcBlockPublicAccessOptions":
		h.describeVPCBPAOptions(w, r, v)
	case "ModifyVpcBlockPublicAccessOptions":
		h.modifyVPCBPAOptions(w, r, v)
	case "CreateVpcBlockPublicAccessExclusion":
		h.createVPCBPAExclusion(w, r, v)
	case "ModifyVpcBlockPublicAccessExclusion":
		h.modifyVPCBPAExclusion(w, r, v)
	case "DeleteVpcBlockPublicAccessExclusion":
		h.deleteVPCBPAExclusion(w, r, v)
	case "DescribeVpcBlockPublicAccessExclusions":
		h.describeVPCBPAExclusions(w, r, v)
	default:
		return false
	}

	return true
}

// ---- XML shapes ----

type vpcBPAOptionsXML struct {
	AwsAccountID             string `xml:"awsAccountId,omitempty"`
	AwsRegion                string `xml:"awsRegion,omitempty"`
	State                    string `xml:"state,omitempty"`
	InternetGatewayBlockMode string `xml:"internetGatewayBlockMode,omitempty"`
	ExclusionsAllowed        string `xml:"exclusionsAllowed,omitempty"`
	ManagedBy                string `xml:"managedBy,omitempty"`
	Reason                   string `xml:"reason,omitempty"`
	LastUpdateTimestamp      string `xml:"lastUpdateTimestamp,omitempty"`
}

type vpcBPAExclusionXML struct {
	ExclusionID                  string    `xml:"exclusionId"`
	InternetGatewayExclusionMode string    `xml:"internetGatewayExclusionMode,omitempty"`
	ResourceArn                  string    `xml:"resourceArn,omitempty"`
	State                        string    `xml:"state,omitempty"`
	Reason                       string    `xml:"reason,omitempty"`
	CreationTimestamp            string    `xml:"creationTimestamp,omitempty"`
	LastUpdateTimestamp          string    `xml:"lastUpdateTimestamp,omitempty"`
	Tags                         []tagItem `xml:"tagSet>item,omitempty"`
}

// ---- options handlers ----

func (*Handler) describeVPCBPAOptions(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	out, err := v.DescribeVPCBlockPublicAccessOptions(r.Context())
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"DescribeVpcBlockPublicAccessOptionsResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Options vpcBPAOptionsXML `xml:"vpcBlockPublicAccessOptions"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Options: toVPCBPAOptionsXML(out)})
}

func (*Handler) modifyVPCBPAOptions(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	out, err := v.ModifyVPCBlockPublicAccessOptions(r.Context(), r.Form.Get("InternetGatewayBlockMode"))
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name         `xml:"ModifyVpcBlockPublicAccessOptionsResponse"`
		Xmlns   string           `xml:"xmlns,attr"`
		Req     string           `xml:"requestId"`
		Options vpcBPAOptionsXML `xml:"vpcBlockPublicAccessOptions"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Options: toVPCBPAOptionsXML(out)})
}

// ---- exclusion handlers ----

func (*Handler) createVPCBPAExclusion(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	out, err := v.CreateVPCBlockPublicAccessExclusion(r.Context(), netdriver.VPCBlockPublicAccessExclusionConfig{
		VPCID:                        r.Form.Get("VpcId"),
		SubnetID:                     r.Form.Get("SubnetId"),
		InternetGatewayExclusionMode: r.Form.Get("InternetGatewayExclusionMode"),
		Tags:                         mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc-block-public-access-exclusion"),
	})
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name           `xml:"CreateVpcBlockPublicAccessExclusionResponse"`
		Xmlns     string             `xml:"xmlns,attr"`
		Req       string             `xml:"requestId"`
		Exclusion vpcBPAExclusionXML `xml:"vpcBlockPublicAccessExclusion"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Exclusion: toVPCBPAExclusionXML(out)})
}

func (*Handler) modifyVPCBPAExclusion(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	out, err := v.ModifyVPCBlockPublicAccessExclusion(r.Context(),
		r.Form.Get("ExclusionId"), r.Form.Get("InternetGatewayExclusionMode"))
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name           `xml:"ModifyVpcBlockPublicAccessExclusionResponse"`
		Xmlns     string             `xml:"xmlns,attr"`
		Req       string             `xml:"requestId"`
		Exclusion vpcBPAExclusionXML `xml:"vpcBlockPublicAccessExclusion"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Exclusion: toVPCBPAExclusionXML(out)})
}

func (*Handler) deleteVPCBPAExclusion(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	out, err := v.DeleteVPCBlockPublicAccessExclusion(r.Context(), r.Form.Get("ExclusionId"))
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name           `xml:"DeleteVpcBlockPublicAccessExclusionResponse"`
		Xmlns     string             `xml:"xmlns,attr"`
		Req       string             `xml:"requestId"`
		Exclusion vpcBPAExclusionXML `xml:"vpcBlockPublicAccessExclusion"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Exclusion: toVPCBPAExclusionXML(out)})
}

//nolint:dupl // parallel per-resource wire dispatch/marshaling
func (*Handler) describeVPCBPAExclusions(w http.ResponseWriter, r *http.Request, v netdriver.VPCBlockPublicAccess) {
	items, err := v.DescribeVPCBlockPublicAccessExclusions(r.Context(), awsquery.ListStrings(r.Form, "ExclusionId"))
	if err != nil {
		writeVPCBPAErr(w, err)
		return
	}

	out := make([]vpcBPAExclusionXML, 0, len(items))
	for i := range items {
		out = append(out, toVPCBPAExclusionXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name             `xml:"DescribeVpcBlockPublicAccessExclusionsResponse"`
		Xmlns   string               `xml:"xmlns,attr"`
		Req     string               `xml:"requestId"`
		Set     []vpcBPAExclusionXML `xml:"vpcBlockPublicAccessExclusionSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

// ---- driver → XML ----

func toVPCBPAOptionsXML(o *netdriver.VPCBlockPublicAccessOptions) vpcBPAOptionsXML {
	return vpcBPAOptionsXML{
		AwsAccountID:             o.AWSAccountID,
		AwsRegion:                o.AWSRegion,
		State:                    o.State,
		InternetGatewayBlockMode: o.InternetGatewayBlockMode,
		ExclusionsAllowed:        o.ExclusionsAllowed,
		ManagedBy:                o.ManagedBy,
		Reason:                   o.Reason,
		LastUpdateTimestamp:      formatTime(o.LastUpdateTimestamp),
	}
}

func toVPCBPAExclusionXML(e *netdriver.VPCBlockPublicAccessExclusion) vpcBPAExclusionXML {
	return vpcBPAExclusionXML{
		ExclusionID:                  e.ExclusionID,
		InternetGatewayExclusionMode: e.InternetGatewayExclusionMode,
		ResourceArn:                  e.ResourceARN,
		State:                        e.State,
		Reason:                       e.Reason,
		CreationTimestamp:            formatTime(e.CreationTimestamp),
		LastUpdateTimestamp:          formatTime(e.LastUpdateTimestamp),
		Tags:                         toTagItems(e.Tags),
	}
}

func writeVPCBPAErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcBlockPublicAccessExclusionId.NotFound", "DependencyViolation")
}
