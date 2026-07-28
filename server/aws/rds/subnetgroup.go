package rds

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

type subnetIdentifierXML struct {
	SubnetIdentifier string `xml:"SubnetIdentifier"`
	SubnetStatus     string `xml:"SubnetStatus"`
}

type dbSubnetGroupXML struct {
	DBSubnetGroupName        string                `xml:"DBSubnetGroupName"`
	DBSubnetGroupDescription string                `xml:"DBSubnetGroupDescription,omitempty"`
	VpcID                    string                `xml:"VpcId,omitempty"`
	SubnetGroupStatus        string                `xml:"SubnetGroupStatus"`
	DBSubnetGroupArn         string                `xml:"DBSubnetGroupArn,omitempty"`
	Subnets                  []subnetIdentifierXML `xml:"Subnets>Subnet,omitempty"`
}

type dbSubnetGroupResult struct {
	DBSubnetGroup dbSubnetGroupXML `xml:"DBSubnetGroup"`
}

type createDBSubnetGroupResponse struct {
	XMLName  xml.Name            `xml:"CreateDBSubnetGroupResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   dbSubnetGroupResult `xml:"CreateDBSubnetGroupResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type describeDBSubnetGroupsResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBSubnetGroupsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   subnetGroupsList `xml:"DescribeDBSubnetGroupsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type subnetGroupsList struct {
	DBSubnetGroups []dbSubnetGroupXML `xml:"DBSubnetGroups>DBSubnetGroup"`
}

type deleteDBSubnetGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBSubnetGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// subnetGroups reports whether the configured driver models subnet groups.
// They are an AWS-only resource, so a driver for another cloud legitimately
// does not, and the honest answer there is InvalidAction rather than a
// fabricated empty group.
func (h *Handler) subnetGroups() (rdsdriver.SubnetGroups, bool) {
	sg, ok := h.db.(rdsdriver.SubnetGroups)

	return sg, ok
}

func (h *Handler) createDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "DB subnet groups")
		return
	}

	sg, err := store.CreateDBSubnetGroup(r.Context(), rdsdriver.SubnetGroupConfig{
		Name:        r.Form.Get("DBSubnetGroupName"),
		Description: r.Form.Get("DBSubnetGroupDescription"),
		SubnetIDs:   awsquery.ListStrings(r.Form, "SubnetIds.SubnetIdentifier"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBSubnetGroupResponse{
		Xmlns:    Namespace,
		Result:   dbSubnetGroupResult{DBSubnetGroup: toSubnetGroupXML(sg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBSubnetGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "DB subnet groups")
		return
	}

	var names []string
	if n := r.Form.Get("DBSubnetGroupName"); n != "" {
		names = []string{n}
	}

	groups, err := store.DescribeDBSubnetGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbSubnetGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toSubnetGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBSubnetGroupsResponse{
		Xmlns:    Namespace,
		Result:   subnetGroupsList{DBSubnetGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBSubnetGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "DB subnet groups")
		return
	}

	if err := store.DeleteDBSubnetGroup(r.Context(), r.Form.Get("DBSubnetGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBSubnetGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func toSubnetGroupXML(sg *rdsdriver.SubnetGroup) dbSubnetGroupXML {
	x := dbSubnetGroupXML{
		DBSubnetGroupName:        sg.Name,
		DBSubnetGroupDescription: sg.Description,
		VpcID:                    sg.VPCID,
		SubnetGroupStatus:        sg.Status,
		DBSubnetGroupArn:         sg.ARN,
	}

	for _, id := range sg.SubnetIDs {
		x.Subnets = append(x.Subnets, subnetIdentifierXML{
			SubnetIdentifier: id,
			SubnetStatus:     "Active",
		})
	}

	return x
}

// writeUnsupported reports a capability this driver does not implement.
//
// The code is InvalidAction because that is what the service answers for an
// operation it does not serve — and because a caller matching on the code sees
// this, not the message. Routing it through the generic error mapping would
// have produced InvalidParameterValue while the message claimed otherwise.
func writeUnsupported(w http.ResponseWriter, what string) {
	awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
		"this driver does not model "+what)
}
