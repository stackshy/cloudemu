package ec2

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// codeInvalidAssociationID is the EC2 error code for a request naming a
// non-existent IAM instance-profile association id.
const codeInvalidAssociationID = "InvalidAssociationID.NotFound"

// Filter names DescribeIamInstanceProfileAssociations understands.
const (
	assocFilterInstanceID = "instance-id"
	assocFilterState      = "state"
)

// iamAssociationXML is the nested <iamInstanceProfileAssociation> element carried
// by the Associate/Replace/Disassociate responses and by each Describe item. The
// child element names (associationId, instanceId, iamInstanceProfile>{arn,id},
// state) match what the aws-sdk-go-v2 deserializer binds to
// types.IamInstanceProfileAssociation.
type iamAssociationXML struct {
	AssociationID      string                 `xml:"associationId"`
	InstanceID         string                 `xml:"instanceId"`
	IamInstanceProfile *iamInstanceProfileXML `xml:"iamInstanceProfile,omitempty"`
	State              string                 `xml:"state"`
}

// iamAssociationResponse is the single-association envelope shared by the
// Associate/Replace/Disassociate responses; only the root element name differs,
// carried in XMLName.
type iamAssociationResponse struct {
	XMLName     xml.Name
	Xmlns       string             `xml:"xmlns,attr"`
	RequestID   string             `xml:"requestId"`
	Association *iamAssociationXML `xml:"iamInstanceProfileAssociation"`
}

type describeIamInstanceProfileAssociationsResponse struct {
	XMLName      xml.Name            `xml:"DescribeIamInstanceProfileAssociationsResponse"`
	Xmlns        string              `xml:"xmlns,attr"`
	RequestID    string              `xml:"requestId"`
	Associations []iamAssociationXML `xml:"iamInstanceProfileAssociationSet>item"`
	NextToken    string              `xml:"nextToken,omitempty"`
}

// routeIamInstanceProfileAssociations dispatches the post-launch IAM
// instance-profile association actions (the counterparts to
// RunInstances{IamInstanceProfile}). Served by the AWS-only
// IamInstanceProfileAssociator capability.
func (h *Handler) routeIamInstanceProfileAssociations(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "AssociateIamInstanceProfile":
		h.associateIamInstanceProfile(w, r)
	case "DescribeIamInstanceProfileAssociations":
		h.describeIamInstanceProfileAssociations(w, r)
	case "ReplaceIamInstanceProfileAssociation":
		h.replaceIamInstanceProfileAssociation(w, r)
	case "DisassociateIamInstanceProfile":
		h.disassociateIamInstanceProfile(w, r)
	default:
		return false
	}

	return true
}

// iamProfileAssociator returns the association capability, writing an
// Unimplemented error and false when the compute driver doesn't provide it.
func (h *Handler) iamProfileAssociator(w http.ResponseWriter) (computedriver.IamInstanceProfileAssociator, bool) {
	assoc, ok := h.compute.(computedriver.IamInstanceProfileAssociator)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented,
			"IAM instance profile associations are not supported"))

		return nil, false
	}

	return assoc, true
}

func (h *Handler) associateIamInstanceProfile(w http.ResponseWriter, r *http.Request) {
	associator, ok := h.iamProfileAssociator(w)
	if !ok {
		return
	}

	out, err := associator.AssociateIamInstanceProfile(r.Context(),
		r.Form.Get("InstanceId"),
		r.Form.Get("IamInstanceProfile.Arn"),
		r.Form.Get("IamInstanceProfile.Name"))
	writeAssociationResult(w, "AssociateIamInstanceProfileResponse", codeInvalidInstanceID, out, err)
}

func (h *Handler) replaceIamInstanceProfileAssociation(w http.ResponseWriter, r *http.Request) {
	associator, ok := h.iamProfileAssociator(w)
	if !ok {
		return
	}

	out, err := associator.ReplaceIamInstanceProfileAssociation(r.Context(),
		r.Form.Get("AssociationId"),
		r.Form.Get("IamInstanceProfile.Arn"),
		r.Form.Get("IamInstanceProfile.Name"))
	writeAssociationResult(w, "ReplaceIamInstanceProfileAssociationResponse", codeInvalidAssociationID, out, err)
}

func (h *Handler) disassociateIamInstanceProfile(w http.ResponseWriter, r *http.Request) {
	associator, ok := h.iamProfileAssociator(w)
	if !ok {
		return
	}

	out, err := associator.DisassociateIamInstanceProfile(r.Context(), r.Form.Get("AssociationId"))
	writeAssociationResult(w, "DisassociateIamInstanceProfileResponse", codeInvalidAssociationID, out, err)
}

// writeAssociationResult writes the single-association response for an
// Associate/Replace/Disassociate action, or the mapped EC2 error. notFoundCode
// selects the resource-specific NotFound code (InvalidInstanceID.NotFound for
// Associate, InvalidAssociationID.NotFound for Replace/Disassociate).
func writeAssociationResult(
	w http.ResponseWriter, root, notFoundCode string,
	out *computedriver.IamInstanceProfileAssociation, err error,
) {
	if err != nil {
		writeErrWithNotFound(w, err, notFoundCode, "IncorrectState")
		return
	}

	awsquery.WriteXMLResponse(w, iamAssociationResponse{
		XMLName:     xml.Name{Local: root},
		Xmlns:       awsquery.Namespace,
		RequestID:   awsquery.RequestID,
		Association: associationXML(out),
	})
}

func (h *Handler) describeIamInstanceProfileAssociations(w http.ResponseWriter, r *http.Request) {
	associator, ok := h.iamProfileAssociator(w)
	if !ok {
		return
	}

	input := computedriver.DescribeIamInstanceProfileAssociationsInput{
		AssociationIDs: awsquery.ListStrings(r.Form, "AssociationId"),
	}

	for _, f := range awsquery.Filters(r.Form) {
		switch f.Name {
		case assocFilterInstanceID:
			input.InstanceIDs = append(input.InstanceIDs, f.Values...)
		case assocFilterState:
			input.States = append(input.States, f.Values...)
		}
	}

	assocs, err := associator.DescribeIamInstanceProfileAssociations(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	items := make([]iamAssociationXML, 0, len(assocs))
	for i := range assocs {
		items = append(items, *associationXML(&assocs[i]))
	}

	page, next := paginateXML(items, r.Form.Get("MaxResults"), r.Form.Get("NextToken"),
		func(a iamAssociationXML) string { return a.AssociationID })

	awsquery.WriteXMLResponse(w, describeIamInstanceProfileAssociationsResponse{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		Associations: page,
		NextToken:    next,
	})
}

// associationXML renders a driver association as its wire element.
func associationXML(a *computedriver.IamInstanceProfileAssociation) *iamAssociationXML {
	if a == nil {
		return nil
	}

	out := &iamAssociationXML{
		AssociationID: a.AssociationID,
		InstanceID:    a.InstanceID,
		State:         a.State,
	}

	if a.Profile != nil {
		out.IamInstanceProfile = &iamInstanceProfileXML{
			ARN: a.Profile.ARN,
			ID:  a.Profile.ID,
		}
	}

	return out
}
