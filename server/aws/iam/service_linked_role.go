package iam

import (
	"context"
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// serviceLinkedRoleCreator is the AWS-specific CreateServiceLinkedRole surface.
// It's not part of the portable IAM driver, so the handler type-asserts for it.
type serviceLinkedRoleCreator interface {
	CreateServiceLinkedRole(
		ctx context.Context, awsServiceName, customSuffix, description string,
	) (*iamdriver.RoleInfo, error)
}

type createServiceLinkedRoleResponse struct {
	XMLName  xml.Name                      `xml:"CreateServiceLinkedRoleResponse"`
	Xmlns    string                        `xml:"xmlns,attr"`
	Result   createServiceLinkedRoleResult `xml:"CreateServiceLinkedRoleResult"`
	Metadata responseMetadata              `xml:"ResponseMetadata"`
}

type createServiceLinkedRoleResult struct {
	Role roleXML `xml:"Role"`
}

func (h *Handler) createServiceLinkedRole(w http.ResponseWriter, r *http.Request) {
	creator, ok := h.iam.(serviceLinkedRoleCreator)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "service-linked roles not supported")
		return
	}

	role, err := creator.CreateServiceLinkedRole(r.Context(),
		r.Form.Get("AWSServiceName"), r.Form.Get("CustomSuffix"), r.Form.Get("Description"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createServiceLinkedRoleResponse{
		Xmlns:    Namespace,
		Result:   createServiceLinkedRoleResult{Role: toRoleXML(role)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
