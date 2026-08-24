package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type eniAttachmentXML struct {
	AttachmentID string `xml:"attachmentId"`
	InstanceID   string `xml:"instanceId,omitempty"`
	DeviceIndex  int    `xml:"deviceIndex"`
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

type createNetworkInterfaceResponseXML struct {
	XMLName          xml.Name            `xml:"CreateNetworkInterfaceResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	RequestID        string              `xml:"requestId"`
	NetworkInterface networkInterfaceXML `xml:"networkInterface"`
}

type attachNetworkInterfaceResponseXML struct {
	XMLName          xml.Name `xml:"AttachNetworkInterfaceResponse"`
	Xmlns            string   `xml:"xmlns,attr"`
	RequestID        string   `xml:"requestId"`
	AttachmentID     string   `xml:"attachmentId"`
	NetworkCardIndex int      `xml:"networkCardIndex"`
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

	store, ok := h.networkInterfaces()
	if !ok {
		writeUnsupportedENI(w)
		return
	}

	enis, err := store.DescribeNetworkInterfaces(r.Context(), ids)
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
	case filterVPCID:
		return eni.VPCID, true
	case "subnet-id":
		return eni.SubnetID, true
	case "status":
		return eni.Status, true
	case "network-interface-id":
		return eni.ID, true
	case filterDescription:
		return eni.Description, true
	case "group-id":
		// ENIs don't model security-group membership, so this filter is
		// recognized but matches no interface. That is what the SG-delete
		// preflight (Terraform lists group-id ENIs before dropping a group)
		// needs: no interface uses the group, so the delete proceeds.
		return "", true
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

func (h *Handler) createNetworkInterface(w http.ResponseWriter, r *http.Request) {
	creator, ok := h.vpc.(netdriver.NetworkInterfaceCreator)
	if !ok {
		writeUnsupportedENI(w)
		return
	}

	subnetID := r.Form.Get("SubnetId")
	if subnetID == "" {
		writeENIErr(w, cerrors.New(cerrors.InvalidArgument, "SubnetId is required"))
		return
	}

	eni, err := creator.CreateNetworkInterface(r.Context(), subnetID, r.Form.Get("Description"),
		awsquery.ListStrings(r.Form, "SecurityGroupId"),
		mergeTagSpecs(awsquery.TagSpecs(r.Form), "network-interface"))
	if err != nil {
		writeENIErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createNetworkInterfaceResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		NetworkInterface: toNetworkInterfaceXML(eni),
	})
}

// attachNetworkInterface attaches an existing ENI to an instance
// (ec2:AttachNetworkInterface). The instance's existence is verified against
// the compute driver here — the networking provider does not model instances —
// so an unknown instance answers InvalidInstanceID.NotFound.
func (h *Handler) attachNetworkInterface(w http.ResponseWriter, r *http.Request) {
	attacher, ok := h.vpc.(netdriver.NetworkInterfaceAttacher)
	if !ok {
		writeUnsupportedENI(w)
		return
	}

	instanceID := r.Form.Get("InstanceId")
	if h.compute != nil {
		insts, err := h.compute.DescribeInstances(r.Context(), []string{instanceID}, nil)
		if err != nil || len(insts) == 0 {
			awsquery.WriteXMLError(w, http.StatusBadRequest, codeInvalidInstanceID,
				"instance "+instanceID+" not found")

			return
		}
	}

	deviceIndex, _ := strconv.Atoi(r.Form.Get("DeviceIndex"))

	attachmentID, err := attacher.AttachNetworkInterface(r.Context(),
		r.Form.Get("NetworkInterfaceId"), instanceID, deviceIndex)
	if err != nil {
		writeENIAttachErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, attachNetworkInterfaceResponseXML{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		AttachmentID: attachmentID,
	})
}

// writeENIAttachErr maps an already-attached interface to InvalidNetworkInterface.InUse
// (real EC2's code), falling back to the shared ENI error mapping otherwise.
func writeENIAttachErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) && strings.Contains(err.Error(), "InvalidNetworkInterface.InUse:") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidNetworkInterface.InUse", err.Error())
		return
	}

	writeENIErr(w, err)
}

func (h *Handler) detachNetworkInterface(w http.ResponseWriter, r *http.Request) {
	force := r.Form.Get("Force") == formTrue

	store, ok := h.networkInterfaces()
	if !ok {
		writeUnsupportedENI(w)
		return
	}

	if err := store.DetachNetworkInterface(r.Context(), r.Form.Get("AttachmentId"), force); err != nil {
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
	store, ok := h.networkInterfaces()
	if !ok {
		writeUnsupportedENI(w)
		return
	}

	if err := store.DeleteNetworkInterface(r.Context(), r.Form.Get("NetworkInterfaceId")); err != nil {
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
		x.Attachment = &eniAttachmentXML{
			AttachmentID: e.AttachmentID,
			InstanceID:   e.InstanceID,
			DeviceIndex:  e.DeviceIndex,
			Status:       stateAttached,
		}
	}

	return x
}

func writeENIErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidNetworkInterfaceID.NotFound", "DependencyViolation")
}

// networkInterfaces reports whether the configured driver models interfaces.
// They are an optional capability, so a driver for a cloud that does not model
// them answers InvalidAction rather than being forced to carry an empty
// implementation.
func (h *Handler) networkInterfaces() (netdriver.NetworkInterfaces, bool) {
	enis, ok := h.vpc.(netdriver.NetworkInterfaces)

	return enis, ok
}

func writeUnsupportedENI(w http.ResponseWriter) {
	awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
		"this driver does not model network interfaces")
}
