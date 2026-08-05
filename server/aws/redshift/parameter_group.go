package redshift

import (
	"encoding/xml"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

type clusterParameterGroupXML struct {
	ParameterGroupName   string `xml:"ParameterGroupName"`
	ParameterGroupFamily string `xml:"ParameterGroupFamily"`
	Description          string `xml:"Description"`
}

type createClusterParameterGroupResponse struct {
	XMLName  xml.Name                 `xml:"CreateClusterParameterGroupResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Group    clusterParameterGroupXML `xml:"CreateClusterParameterGroupResult>ClusterParameterGroup"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type clusterSubnetGroupXML struct {
	ClusterSubnetGroupName string `xml:"ClusterSubnetGroupName"`
	Description            string `xml:"Description"`
	SubnetGroupStatus      string `xml:"SubnetGroupStatus"`
}

type createClusterSubnetGroupResponse struct {
	XMLName  xml.Name              `xml:"CreateClusterSubnetGroupResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Group    clusterSubnetGroupXML `xml:"CreateClusterSubnetGroupResult>ClusterSubnetGroup"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

func (h *Handler) clusterGroups() (clusterGroupManager, bool) {
	m, ok := h.db.(clusterGroupManager)

	return m, ok
}

func (h *Handler) createClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	pg, err := mgr.CreateClusterParameterGroup(r.Context(),
		r.Form.Get("ParameterGroupName"), r.Form.Get("ParameterGroupFamily"), r.Form.Get("Description"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createClusterParameterGroupResponse{
		Xmlns: Namespace,
		Group: clusterParameterGroupXML{
			ParameterGroupName:   pg.Name,
			ParameterGroupFamily: pg.Family,
			Description:          pg.Description,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) createClusterSubnetGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "subnet groups not supported"))
		return
	}

	sg, err := mgr.CreateClusterSubnetGroup(r.Context(),
		r.Form.Get("ClusterSubnetGroupName"), r.Form.Get("Description"),
		awsquery.ListStrings(r.Form, "SubnetIds.SubnetIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createClusterSubnetGroupResponse{
		Xmlns: Namespace,
		Group: clusterSubnetGroupXML{
			ClusterSubnetGroupName: sg.Name,
			Description:            sg.Description,
			SubnetGroupStatus:      "Complete",
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
