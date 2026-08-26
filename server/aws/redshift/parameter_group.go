package redshift

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	redshiftprovider "github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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

type subnetAvailabilityZoneXML struct {
	Name string `xml:"Name,omitempty"`
}

type subnetXML struct {
	SubnetIdentifier       string                    `xml:"SubnetIdentifier"`
	SubnetAvailabilityZone subnetAvailabilityZoneXML `xml:"SubnetAvailabilityZone"`
	SubnetStatus           string                    `xml:"SubnetStatus"`
}

type subnetsXML struct {
	Subnet []subnetXML `xml:"Subnet,omitempty"`
}

type clusterSubnetGroupXML struct {
	ClusterSubnetGroupName string      `xml:"ClusterSubnetGroupName"`
	Description            string      `xml:"Description"`
	VpcID                  string      `xml:"VpcId,omitempty"`
	SubnetGroupStatus      string      `xml:"SubnetGroupStatus"`
	Subnets                *subnetsXML `xml:"Subnets,omitempty"`
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

type redshiftParameterXML struct {
	ParameterName  string `xml:"ParameterName"`
	ParameterValue string `xml:"ParameterValue,omitempty"`
	Description    string `xml:"Description,omitempty"`
	Source         string `xml:"Source,omitempty"`
	DataType       string `xml:"DataType,omitempty"`
	ApplyType      string `xml:"ApplyType,omitempty"`
	IsModifiable   bool   `xml:"IsModifiable"`
}

type modifyClusterParameterGroupResponse struct {
	XMLName              xml.Name         `xml:"ModifyClusterParameterGroupResponse"`
	Xmlns                string           `xml:"xmlns,attr"`
	ParameterGroupName   string           `xml:"ModifyClusterParameterGroupResult>ParameterGroupName"`
	ParameterGroupStatus string           `xml:"ModifyClusterParameterGroupResult>ParameterGroupStatus"`
	Metadata             responseMetadata `xml:"ResponseMetadata"`
}

type resetClusterParameterGroupResponse struct {
	XMLName              xml.Name         `xml:"ResetClusterParameterGroupResponse"`
	Xmlns                string           `xml:"xmlns,attr"`
	ParameterGroupName   string           `xml:"ResetClusterParameterGroupResult>ParameterGroupName"`
	ParameterGroupStatus string           `xml:"ResetClusterParameterGroupResult>ParameterGroupStatus"`
	Metadata             responseMetadata `xml:"ResponseMetadata"`
}

type describeClusterParametersResponse struct {
	XMLName    xml.Name               `xml:"DescribeClusterParametersResponse"`
	Xmlns      string                 `xml:"xmlns,attr"`
	Parameters []redshiftParameterXML `xml:"DescribeClusterParametersResult>Parameters>Parameter"`
	Metadata   responseMetadata       `xml:"ResponseMetadata"`
}

// paramGroupUpdatedStatus is the human-readable status real Redshift returns
// from ModifyClusterParameterGroup / ResetClusterParameterGroup.
const paramGroupUpdatedStatus = "Your parameter group has been updated but changes " +
	"won't get applied until you reboot the associated clusters."

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

func (h *Handler) modifyClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	name := r.Form.Get("ParameterGroupName")

	if _, err := mgr.ModifyClusterParameterGroup(r.Context(), name, parseRedshiftParameters(r.Form)); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyClusterParameterGroupResponse{
		Xmlns:                Namespace,
		ParameterGroupName:   name,
		ParameterGroupStatus: paramGroupUpdatedStatus,
		Metadata:             responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeClusterParameters(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	params, err := mgr.DescribeClusterParameters(r.Context(), r.Form.Get("ParameterGroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]redshiftParameterXML, 0, len(params))
	for i := range params {
		out = append(out, redshiftParameterXML{
			ParameterName:  params[i].Name,
			ParameterValue: params[i].Value,
			Description:    params[i].Description,
			Source:         params[i].Source,
			DataType:       params[i].DataType,
			ApplyType:      params[i].ApplyType,
			IsModifiable:   true,
		})
	}

	awsquery.WriteXMLResponse(w, describeClusterParametersResponse{
		Xmlns:      Namespace,
		Parameters: out,
		Metadata:   responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) resetClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.clusterGroups()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "parameter groups not supported"))
		return
	}

	name := r.Form.Get("ParameterGroupName")
	resetAll := formBool(r.Form.Get("ResetAllParameters"))

	if _, err := mgr.ResetClusterParameterGroup(r.Context(), name, parseRedshiftParameterNames(r.Form), resetAll); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, resetClusterParameterGroupResponse{
		Xmlns:                Namespace,
		ParameterGroupName:   name,
		ParameterGroupStatus: paramGroupUpdatedStatus,
		Metadata:             responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseRedshiftParameters reads Parameters.Parameter.N.{ParameterName,ParameterValue}.
func parseRedshiftParameters(form url.Values) []rdbdriver.Parameter {
	indices := awsquery.CollectIndices(form, "Parameters.Parameter")
	if len(indices) == 0 {
		return nil
	}

	out := make([]rdbdriver.Parameter, 0, len(indices))

	for _, n := range indices {
		base := "Parameters.Parameter." + strconv.Itoa(n)

		pname := form.Get(base + ".ParameterName")
		if pname == "" {
			continue
		}

		out = append(out, rdbdriver.Parameter{
			Name:  pname,
			Value: form.Get(base + ".ParameterValue"),
		})
	}

	return out
}

// parseRedshiftParameterNames reads just the ParameterName entries (used by Reset).
func parseRedshiftParameterNames(form url.Values) []string {
	params := parseRedshiftParameters(form)

	names := make([]string, 0, len(params))
	for i := range params {
		names = append(names, params[i].Name)
	}

	return names
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
		Xmlns:    Namespace,
		Group:    toClusterSubnetGroupXML(sg),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// toClusterSubnetGroupXML renders a provider subnet group into the wire shape,
// emitting the derived VpcId and the full Subnets list (each with its
// availability zone) real Redshift returns.
func toClusterSubnetGroupXML(sg *redshiftprovider.SubnetGroup) clusterSubnetGroupXML {
	out := clusterSubnetGroupXML{
		ClusterSubnetGroupName: sg.Name,
		Description:            sg.Description,
		VpcID:                  sg.VPCID,
		SubnetGroupStatus:      "Complete",
	}

	if len(sg.Subnets) > 0 {
		subnets := &subnetsXML{Subnet: make([]subnetXML, 0, len(sg.Subnets))}
		for _, s := range sg.Subnets {
			subnets.Subnet = append(subnets.Subnet, subnetXML{
				SubnetIdentifier:       s.ID,
				SubnetAvailabilityZone: subnetAvailabilityZoneXML{Name: s.AvailabilityZone},
				SubnetStatus:           "Active",
			})
		}

		out.Subnets = subnets
	}

	return out
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
		out = append(out, toClusterSubnetGroupXML(&groups[i]))
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
