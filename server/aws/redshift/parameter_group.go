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

type describeClusterParameterGroupsResponse struct {
	XMLName  xml.Name                   `xml:"DescribeClusterParameterGroupsResponse"`
	Xmlns    string                     `xml:"xmlns,attr"`
	Groups   []clusterParameterGroupXML `xml:"DescribeClusterParameterGroupsResult>ParameterGroups>ClusterParameterGroup"`
	Metadata responseMetadata           `xml:"ResponseMetadata"`
}

type deleteClusterParameterGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteClusterParameterGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeClusterSubnetGroupsResponse struct {
	XMLName  xml.Name                `xml:"DescribeClusterSubnetGroupsResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Groups   []clusterSubnetGroupXML `xml:"DescribeClusterSubnetGroupsResult>ClusterSubnetGroups>ClusterSubnetGroup"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type deleteClusterSubnetGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteClusterSubnetGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
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

func (h *Handler) describeClusterParameterGroups(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	var names []string
	if name := r.Form.Get("ParameterGroupName"); name != "" {
		names = []string{name}
	}

	groups, err := mgr.DescribeClusterParameterGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]clusterParameterGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, clusterParameterGroupXML{
			ParameterGroupName:   groups[i].Name,
			ParameterGroupFamily: groups[i].Family,
			Description:          groups[i].Description,
		})
	}

	awsquery.WriteXMLResponse(w, describeClusterParameterGroupsResponse{
		Xmlns:    Namespace,
		Groups:   out,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	if err := mgr.DeleteClusterParameterGroup(r.Context(), r.Form.Get("ParameterGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeClusterSubnetGroups(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "subnet groups not supported"))
		return
	}

	var names []string
	if name := r.Form.Get("ClusterSubnetGroupName"); name != "" {
		names = []string{name}
	}

	groups, err := mgr.DescribeClusterSubnetGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]clusterSubnetGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, clusterSubnetGroupXML{
			ClusterSubnetGroupName: groups[i].Name,
			Description:            groups[i].Description,
			SubnetGroupStatus:      "Complete",
		})
	}

	awsquery.WriteXMLResponse(w, describeClusterSubnetGroupsResponse{
		Xmlns:    Namespace,
		Groups:   out,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteClusterSubnetGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "subnet groups not supported"))
		return
	}

	if err := mgr.DeleteClusterSubnetGroup(r.Context(), r.Form.Get("ClusterSubnetGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteClusterSubnetGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
