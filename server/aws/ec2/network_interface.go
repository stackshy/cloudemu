package ec2

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type eniAttachmentXML struct {
	AttachmentID string `xml:"attachmentId"`
	Status       string `xml:"status"`
}

type networkInterfaceXML struct {
	NetworkInterfaceID string            `xml:"networkInterfaceId"`
	VpcID              string            `xml:"vpcId,omitempty"`
	SubnetID           string            `xml:"subnetId,omitempty"`
	Status             string            `xml:"status"`
	Description        string            `xml:"description,omitempty"`
	Attachment         *eniAttachmentXML `xml:"attachment,omitempty"`
	Tags               []tagItem         `xml:"tagSet>item,omitempty"`
}

type describeNetworkInterfacesResponseXML struct {
	XMLName             xml.Name              `xml:"DescribeNetworkInterfacesResponse"`
	Xmlns               string                `xml:"xmlns,attr"`
	RequestID           string                `xml:"requestId"`
	NetworkInterfaceSet []networkInterfaceXML `xml:"networkInterfaceSet>item"`
}

type detachNetworkInterfaceResponseXML struct {
	XMLName   xml.Name `xml:"DetachNetworkInterfaceResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type deleteNetworkInterfaceResponseXML struct {
	XMLName   xml.Name `xml:"DeleteNetworkInterfaceResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// describeNetworkInterfaces answers by id and by the filters listed in
// eniFilterField. Filters it does not implement are rejected rather than
// ignored — see validateENIFilters.
func (h *Handler) describeNetworkInterfaces(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "NetworkInterfaceId")
	filters := awsquery.Filters(r.Form)

	if err := validateENIFilters(filters); err != nil {
		writeENIErr(w, err)
		return
	}

	enis, err := h.vpc.DescribeNetworkInterfaces(r.Context(), ids)
	if err != nil {
		writeENIErr(w, err)
		return
	}

	out := make([]networkInterfaceXML, 0, len(enis))

	for i := range enis {
		if !eniMatchesFilters(&enis[i], filters) {
			continue
		}

		out = append(out, toNetworkInterfaceXML(&enis[i]))
	}

	awsquery.WriteXMLResponse(w, describeNetworkInterfacesResponseXML{
		Xmlns:               awsquery.Namespace,
		RequestID:           awsquery.RequestID,
		NetworkInterfaceSet: out,
	})
}

// eniFilterField maps a supported filter name to the field it selects on.
// The second result reports whether the filter is recognized at all.
func eniFilterField(eni *netdriver.NetworkInterface, name string) (string, bool) {
	switch name {
	case "vpc-id":
		return eni.VPCID, true
	case "subnet-id":
		return eni.SubnetID, true
	case "status":
		return eni.Status, true
	case "network-interface-id":
		return eni.ID, true
	case "description":
		return eni.Description, true
	default:
		return "", false
	}
}

// validateENIFilters rejects filter names this emulator does not implement.
//
// Real EC2 answers InvalidParameterValue for an unrecognized filter, and that
// is the safe behavior to copy: silently returning nothing would tell a
// caller draining a VPC that there is nothing left to drain, so it would
// proceed to a VPC delete that then fails with DependencyViolation. Matching
// everything instead is equally bad — it hands back interfaces the caller
// never asked for and may delete. An explicit error is the only answer that
// cannot be mistaken for a result.
func validateENIFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if _, ok := eniFilterField(&netdriver.NetworkInterface{}, f.Name); !ok {
			return cerrors.Newf(cerrors.InvalidArgument,
				"The filter '%s' is invalid", f.Name)
		}
	}

	return nil
}

func eniMatchesFilters(eni *netdriver.NetworkInterface, filters []awsquery.Filter) bool {
	for _, f := range filters {
		field, ok := eniFilterField(eni, f.Name)
		if !ok {
			return false
		}

		if !containsString(f.Values, field) {
			return false
		}
	}

	return true
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}

	return false
}

func (h *Handler) detachNetworkInterface(w http.ResponseWriter, r *http.Request) {
	force := r.Form.Get("Force") == "true"

	if err := h.vpc.DetachNetworkInterface(r.Context(), r.Form.Get("AttachmentId"), force); err != nil {
		writeENIErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, detachNetworkInterfaceResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func (h *Handler) deleteNetworkInterface(w http.ResponseWriter, r *http.Request) {
	if err := h.vpc.DeleteNetworkInterface(r.Context(), r.Form.Get("NetworkInterfaceId")); err != nil {
		writeENIErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteNetworkInterfaceResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

func toNetworkInterfaceXML(e *netdriver.NetworkInterface) networkInterfaceXML {
	x := networkInterfaceXML{
		NetworkInterfaceID: e.ID,
		VpcID:              e.VPCID,
		SubnetID:           e.SubnetID,
		Status:             nonEmpty(e.Status, "available"),
		Description:        e.Description,
		Tags:               toTagItems(e.Tags),
	}

	if e.AttachmentID != "" {
		x.Attachment = &eniAttachmentXML{AttachmentID: e.AttachmentID, Status: "attached"}
	}

	return x
}

func writeENIErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidNetworkInterfaceID.NotFound", "DependencyViolation")
}
