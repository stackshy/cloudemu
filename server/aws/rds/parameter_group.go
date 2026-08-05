package rds

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- XML shapes ----

type dbParameterGroupXML struct {
	DBParameterGroupName   string `xml:"DBParameterGroupName"`
	DBParameterGroupFamily string `xml:"DBParameterGroupFamily,omitempty"`
	Description            string `xml:"Description,omitempty"`
	DBParameterGroupArn    string `xml:"DBParameterGroupArn,omitempty"`
}

type dbClusterParameterGroupXML struct {
	DBClusterParameterGroupName string `xml:"DBClusterParameterGroupName"`
	DBParameterGroupFamily      string `xml:"DBParameterGroupFamily,omitempty"`
	Description                 string `xml:"Description,omitempty"`
	DBClusterParameterGroupArn  string `xml:"DBClusterParameterGroupArn,omitempty"`
}

type parameterXML struct {
	ParameterName  string `xml:"ParameterName"`
	ParameterValue string `xml:"ParameterValue,omitempty"`
	ApplyMethod    string `xml:"ApplyMethod,omitempty"`
	Source         string `xml:"Source,omitempty"`
	ApplyType      string `xml:"ApplyType,omitempty"`
	DataType       string `xml:"DataType,omitempty"`
	Description    string `xml:"Description,omitempty"`
}

type createDBParameterGroupResponse struct {
	XMLName  xml.Name            `xml:"CreateDBParameterGroupResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   dbParameterGroupRes `xml:"CreateDBParameterGroupResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type copyDBParameterGroupResponse struct {
	XMLName  xml.Name            `xml:"CopyDBParameterGroupResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   dbParameterGroupRes `xml:"CopyDBParameterGroupResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type dbParameterGroupRes struct {
	DBParameterGroup dbParameterGroupXML `xml:"DBParameterGroup"`
}

type describeDBParameterGroupsResponse struct {
	XMLName  xml.Name              `xml:"DescribeDBParameterGroupsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   dbParameterGroupsList `xml:"DescribeDBParameterGroupsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type dbParameterGroupsList struct {
	DBParameterGroups []dbParameterGroupXML `xml:"DBParameterGroups>DBParameterGroup"`
}

type modifyDBParameterGroupResponse struct {
	XMLName  xml.Name          `xml:"ModifyDBParameterGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   paramGroupNameRes `xml:"ModifyDBParameterGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type resetDBParameterGroupResponse struct {
	XMLName  xml.Name          `xml:"ResetDBParameterGroupResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   paramGroupNameRes `xml:"ResetDBParameterGroupResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type paramGroupNameRes struct {
	DBParameterGroupName string `xml:"DBParameterGroupName"`
}

type deleteDBParameterGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBParameterGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeDBParametersResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBParametersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   parametersList   `xml:"DescribeDBParametersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type parametersList struct {
	Parameters []parameterXML `xml:"Parameters>Parameter"`
}

type createDBClusterParameterGroupResponse struct {
	XMLName  xml.Name                   `xml:"CreateDBClusterParameterGroupResponse"`
	Xmlns    string                     `xml:"xmlns,attr"`
	Result   dbClusterParameterGroupRes `xml:"CreateDBClusterParameterGroupResult"`
	Metadata responseMetadata           `xml:"ResponseMetadata"`
}

type copyDBClusterParameterGroupResponse struct {
	XMLName  xml.Name                   `xml:"CopyDBClusterParameterGroupResponse"`
	Xmlns    string                     `xml:"xmlns,attr"`
	Result   dbClusterParameterGroupRes `xml:"CopyDBClusterParameterGroupResult"`
	Metadata responseMetadata           `xml:"ResponseMetadata"`
}

type dbClusterParameterGroupRes struct {
	DBClusterParameterGroup dbClusterParameterGroupXML `xml:"DBClusterParameterGroup"`
}

type describeDBClusterParameterGroupsResponse struct {
	XMLName  xml.Name                     `xml:"DescribeDBClusterParameterGroupsResponse"`
	Xmlns    string                       `xml:"xmlns,attr"`
	Result   dbClusterParameterGroupsList `xml:"DescribeDBClusterParameterGroupsResult"`
	Metadata responseMetadata             `xml:"ResponseMetadata"`
}

type dbClusterParameterGroupsList struct {
	DBClusterParameterGroups []dbClusterParameterGroupXML `xml:"DBClusterParameterGroups>DBClusterParameterGroup"`
}

type modifyDBClusterParameterGroupResponse struct {
	XMLName  xml.Name                 `xml:"ModifyDBClusterParameterGroupResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   clusterParamGroupNameRes `xml:"ModifyDBClusterParameterGroupResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type resetDBClusterParameterGroupResponse struct {
	XMLName  xml.Name                 `xml:"ResetDBClusterParameterGroupResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   clusterParamGroupNameRes `xml:"ResetDBClusterParameterGroupResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

type clusterParamGroupNameRes struct {
	DBClusterParameterGroupName string `xml:"DBClusterParameterGroupName"`
}

type deleteDBClusterParameterGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBClusterParameterGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeDBClusterParametersResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBClusterParametersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   parametersList   `xml:"DescribeDBClusterParametersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// ---- helpers ----

// parameterGroupsCap reports whether the driver models parameter groups.
func (h *Handler) parameterGroupsCap() (rdsdriver.ParameterGroups, bool) {
	pg, ok := h.db.(rdsdriver.ParameterGroups)

	return pg, ok
}

// parseParameters reads Parameters.Parameter.N.{ParameterName,ParameterValue,ApplyMethod}.
func parseParameters(form url.Values) []rdsdriver.Parameter {
	indices := awsquery.CollectIndices(form, "Parameters.Parameter")
	if len(indices) == 0 {
		return nil
	}

	out := make([]rdsdriver.Parameter, 0, len(indices))

	for _, n := range indices {
		base := "Parameters.Parameter." + strconv.Itoa(n)

		name := form.Get(base + ".ParameterName")
		if name == "" {
			continue
		}

		out = append(out, rdsdriver.Parameter{
			Name:        name,
			Value:       form.Get(base + ".ParameterValue"),
			ApplyMethod: form.Get(base + ".ApplyMethod"),
		})
	}

	return out
}

// parseParameterNames reads just the ParameterName entries (used by Reset).
func parseParameterNames(form url.Values) []string {
	params := parseParameters(form)

	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name)
	}

	return names
}

func toParameterGroupXML(pg *rdsdriver.ParameterGroup) dbParameterGroupXML {
	return dbParameterGroupXML{
		DBParameterGroupName:   pg.Name,
		DBParameterGroupFamily: pg.Family,
		Description:            pg.Description,
		DBParameterGroupArn:    pg.ARN,
	}
}

func toClusterParameterGroupXML(pg *rdsdriver.ClusterParameterGroup) dbClusterParameterGroupXML {
	return dbClusterParameterGroupXML{
		DBClusterParameterGroupName: pg.Name,
		DBParameterGroupFamily:      pg.Family,
		Description:                 pg.Description,
		DBClusterParameterGroupArn:  pg.ARN,
	}
}

func toParametersXML(params []rdsdriver.Parameter) []parameterXML {
	out := make([]parameterXML, 0, len(params))
	for _, p := range params {
		out = append(out, parameterXML{
			ParameterName:  p.Name,
			ParameterValue: p.Value,
			ApplyMethod:    p.ApplyMethod,
			Source:         p.Source,
			ApplyType:      p.ApplyType,
			DataType:       p.DataType,
			Description:    p.Description,
		})
	}

	return out
}

// ---- DB parameter group handlers ----

func (h *Handler) createDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	pg, err := store.CreateDBParameterGroup(r.Context(), rdsdriver.ParameterGroupConfig{
		Name:        r.Form.Get("DBParameterGroupName"),
		Family:      r.Form.Get("DBParameterGroupFamily"),
		Description: r.Form.Get("Description"),
		Tags:        parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   dbParameterGroupRes{DBParameterGroup: toParameterGroupXML(pg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) describeDBParameterGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	var names []string
	if n := r.Form.Get("DBParameterGroupName"); n != "" {
		names = []string{n}
	}

	groups, err := store.DescribeDBParameterGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbParameterGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toParameterGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBParameterGroupsResponse{
		Xmlns:    Namespace,
		Result:   dbParameterGroupsList{DBParameterGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	name := r.Form.Get("DBParameterGroupName")

	if _, err := store.ModifyDBParameterGroup(r.Context(), name, parseParameters(r.Form)); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   paramGroupNameRes{DBParameterGroupName: name},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	if err := store.DeleteDBParameterGroup(r.Context(), r.Form.Get("DBParameterGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBParameterGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBParameters(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	params, err := store.DescribeDBParameters(r.Context(), r.Form.Get("DBParameterGroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeDBParametersResponse{
		Xmlns:    Namespace,
		Result:   parametersList{Parameters: toParametersXML(params)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) resetDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	name := r.Form.Get("DBParameterGroupName")

	_, err := store.ResetDBParameterGroup(r.Context(), name,
		parseParameterNames(r.Form), formBool(r.Form.Get("ResetAllParameters")))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, resetDBParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   paramGroupNameRes{DBParameterGroupName: name},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) copyDBParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB parameter groups")
		return
	}

	pg, err := store.CopyDBParameterGroup(r.Context(),
		r.Form.Get("SourceDBParameterGroupIdentifier"),
		r.Form.Get("TargetDBParameterGroupIdentifier"),
		r.Form.Get("TargetDBParameterGroupDescription"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copyDBParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   dbParameterGroupRes{DBParameterGroup: toParameterGroupXML(pg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// ---- DB cluster parameter group handlers ----

func (h *Handler) createDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	pg, err := store.CreateDBClusterParameterGroup(r.Context(), rdsdriver.ParameterGroupConfig{
		Name:        r.Form.Get("DBClusterParameterGroupName"),
		Family:      r.Form.Get("DBParameterGroupFamily"),
		Description: r.Form.Get("Description"),
		Tags:        parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   dbClusterParameterGroupRes{DBClusterParameterGroup: toClusterParameterGroupXML(pg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors the other describe-list wire handlers by design.
func (h *Handler) describeDBClusterParameterGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	var names []string
	if n := r.Form.Get("DBClusterParameterGroupName"); n != "" {
		names = []string{n}
	}

	groups, err := store.DescribeDBClusterParameterGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbClusterParameterGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toClusterParameterGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBClusterParameterGroupsResponse{
		Xmlns:    Namespace,
		Result:   dbClusterParameterGroupsList{DBClusterParameterGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	name := r.Form.Get("DBClusterParameterGroupName")

	if _, err := store.ModifyDBClusterParameterGroup(r.Context(), name, parseParameters(r.Form)); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   clusterParamGroupNameRes{DBClusterParameterGroupName: name},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	if err := store.DeleteDBClusterParameterGroup(r.Context(), r.Form.Get("DBClusterParameterGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBClusterParameters(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	params, err := store.DescribeDBClusterParameters(r.Context(), r.Form.Get("DBClusterParameterGroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeDBClusterParametersResponse{
		Xmlns:    Namespace,
		Result:   parametersList{Parameters: toParametersXML(params)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) resetDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	name := r.Form.Get("DBClusterParameterGroupName")

	_, err := store.ResetDBClusterParameterGroup(r.Context(), name,
		parseParameterNames(r.Form), formBool(r.Form.Get("ResetAllParameters")))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, resetDBClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   clusterParamGroupNameRes{DBClusterParameterGroupName: name},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) copyDBClusterParameterGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.parameterGroupsCap()
	if !ok {
		writeUnsupported(w, "DB cluster parameter groups")
		return
	}

	pg, err := store.CopyDBClusterParameterGroup(r.Context(),
		r.Form.Get("SourceDBClusterParameterGroupIdentifier"),
		r.Form.Get("TargetDBClusterParameterGroupIdentifier"),
		r.Form.Get("TargetDBClusterParameterGroupDescription"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copyDBClusterParameterGroupResponse{
		Xmlns:    Namespace,
		Result:   dbClusterParameterGroupRes{DBClusterParameterGroup: toClusterParameterGroupXML(pg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
