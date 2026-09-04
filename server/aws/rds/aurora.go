package rds

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// ---- cluster endpoint XML ----

type dbClusterEndpointXML struct {
	DBClusterEndpointIdentifier string   `xml:"DBClusterEndpointIdentifier"`
	DBClusterIdentifier         string   `xml:"DBClusterIdentifier,omitempty"`
	DBClusterEndpointArn        string   `xml:"DBClusterEndpointArn,omitempty"`
	Endpoint                    string   `xml:"Endpoint,omitempty"`
	Status                      string   `xml:"Status,omitempty"`
	EndpointType                string   `xml:"EndpointType,omitempty"`
	CustomEndpointType          string   `xml:"CustomEndpointType,omitempty"`
	StaticMembers               []string `xml:"StaticMembers>member,omitempty"`
	ExcludedMembers             []string `xml:"ExcludedMembers>member,omitempty"`
}

type createDBClusterEndpointResponse struct {
	XMLName  xml.Name             `xml:"CreateDBClusterEndpointResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   dbClusterEndpointXML `xml:"CreateDBClusterEndpointResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type modifyDBClusterEndpointResponse struct {
	XMLName  xml.Name             `xml:"ModifyDBClusterEndpointResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   dbClusterEndpointXML `xml:"ModifyDBClusterEndpointResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type deleteDBClusterEndpointResponse struct {
	XMLName  xml.Name             `xml:"DeleteDBClusterEndpointResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   dbClusterEndpointXML `xml:"DeleteDBClusterEndpointResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type describeDBClusterEndpointsResponse struct {
	XMLName  xml.Name               `xml:"DescribeDBClusterEndpointsResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   dbClusterEndpointsList `xml:"DescribeDBClusterEndpointsResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type dbClusterEndpointsList struct {
	DBClusterEndpoints []dbClusterEndpointXML `xml:"DBClusterEndpoints>DBClusterEndpointList"`
}

// ---- failover XML ----

type failoverDBClusterResponse struct {
	XMLName  xml.Name         `xml:"FailoverDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"FailoverDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// ---- cluster IAM role association XML ----
//
// AddRoleToDBCluster / RemoveRoleFromDBCluster return an empty result body in
// real RDS; the effect is observed via DescribeDBClusters AssociatedRoles.

type addRoleToDBClusterResponse struct {
	XMLName  xml.Name         `xml:"AddRoleToDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"AddRoleToDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type removeRoleFromDBClusterResponse struct {
	XMLName  xml.Name         `xml:"RemoveRoleFromDBClusterResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"RemoveRoleFromDBClusterResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// ---- global cluster XML ----

type globalClusterMemberXML struct {
	DBClusterArn string `xml:"DBClusterArn,omitempty"`
	IsWriter     bool   `xml:"IsWriter"`
}

type globalClusterXML struct {
	GlobalClusterIdentifier string                   `xml:"GlobalClusterIdentifier"`
	GlobalClusterArn        string                   `xml:"GlobalClusterArn,omitempty"`
	Engine                  string                   `xml:"Engine,omitempty"`
	EngineVersion           string                   `xml:"EngineVersion,omitempty"`
	Status                  string                   `xml:"Status,omitempty"`
	GlobalClusterMembers    []globalClusterMemberXML `xml:"GlobalClusterMembers>GlobalClusterMember,omitempty"`
}

type globalClusterResult struct {
	GlobalCluster globalClusterXML `xml:"GlobalCluster"`
}

type createGlobalClusterResponse struct {
	XMLName  xml.Name            `xml:"CreateGlobalClusterResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   globalClusterResult `xml:"CreateGlobalClusterResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type modifyGlobalClusterResponse struct {
	XMLName  xml.Name            `xml:"ModifyGlobalClusterResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   globalClusterResult `xml:"ModifyGlobalClusterResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type deleteGlobalClusterResponse struct {
	XMLName  xml.Name            `xml:"DeleteGlobalClusterResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   globalClusterResult `xml:"DeleteGlobalClusterResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type removeFromGlobalClusterResponse struct {
	XMLName  xml.Name            `xml:"RemoveFromGlobalClusterResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   globalClusterResult `xml:"RemoveFromGlobalClusterResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type describeGlobalClustersResponse struct {
	XMLName  xml.Name          `xml:"DescribeGlobalClustersResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   globalClusterList `xml:"DescribeGlobalClustersResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

type globalClusterList struct {
	GlobalClusters []globalClusterXML `xml:"GlobalClusters>GlobalClusterMember"`
}

// ---- capability gates ----

func (h *Handler) clusterEndpointsCap() (rdsdriver.ClusterEndpoints, bool) {
	c, ok := h.db.(rdsdriver.ClusterEndpoints)

	return c, ok
}

func (h *Handler) clusterFailoverCap() (rdsdriver.ClusterFailover, bool) {
	c, ok := h.db.(rdsdriver.ClusterFailover)

	return c, ok
}

func (h *Handler) clusterRolesCap() (rdsdriver.ClusterRoles, bool) {
	c, ok := h.db.(rdsdriver.ClusterRoles)

	return c, ok
}

func (h *Handler) globalClustersCap() (rdsdriver.GlobalClusters, bool) {
	c, ok := h.db.(rdsdriver.GlobalClusters)

	return c, ok
}

func toClusterEndpointXML(ep *rdsdriver.ClusterEndpoint) dbClusterEndpointXML {
	return dbClusterEndpointXML{
		DBClusterEndpointIdentifier: ep.EndpointID,
		DBClusterIdentifier:         ep.ClusterID,
		DBClusterEndpointArn:        ep.ARN,
		Endpoint:                    ep.Endpoint,
		Status:                      ep.Status,
		EndpointType:                ep.EndpointType,
		CustomEndpointType:          ep.CustomEndpointType,
		StaticMembers:               ep.StaticMembers,
		ExcludedMembers:             ep.ExcludedMembers,
	}
}

func toGlobalClusterXML(gc *rdsdriver.GlobalCluster) globalClusterXML {
	x := globalClusterXML{
		GlobalClusterIdentifier: gc.ID,
		GlobalClusterArn:        gc.ARN,
		Engine:                  gc.Engine,
		EngineVersion:           gc.EngineVersion,
		Status:                  gc.Status,
	}

	for _, mem := range gc.Members {
		x.GlobalClusterMembers = append(x.GlobalClusterMembers, globalClusterMemberXML{
			DBClusterArn: mem.DBClusterARN,
			IsWriter:     mem.IsWriter,
		})
	}

	return x
}

// ---- cluster endpoint handlers ----

func (h *Handler) createDBClusterEndpoint(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterEndpointsCap()
	if !ok {
		writeUnsupported(w, "custom cluster endpoints")
		return
	}

	ep, err := store.CreateDBClusterEndpoint(r.Context(), rdsdriver.ClusterEndpointConfig{
		EndpointID:      r.Form.Get("DBClusterEndpointIdentifier"),
		ClusterID:       r.Form.Get("DBClusterIdentifier"),
		EndpointType:    r.Form.Get("EndpointType"),
		StaticMembers:   awsquery.ListStrings(r.Form, "StaticMembers.member"),
		ExcludedMembers: awsquery.ListStrings(r.Form, "ExcludedMembers.member"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBClusterEndpointResponse{
		Xmlns:    Namespace,
		Result:   toClusterEndpointXML(ep),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBClusterEndpoints(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterEndpointsCap()
	if !ok {
		writeUnsupported(w, "custom cluster endpoints")
		return
	}

	eps, err := store.DescribeDBClusterEndpoints(r.Context(),
		r.Form.Get("DBClusterIdentifier"), r.Form.Get("DBClusterEndpointIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbClusterEndpointXML, 0, len(eps))
	for i := range eps {
		out = append(out, toClusterEndpointXML(&eps[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBClusterEndpointsResponse{
		Xmlns:    Namespace,
		Result:   dbClusterEndpointsList{DBClusterEndpoints: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBClusterEndpoint(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterEndpointsCap()
	if !ok {
		writeUnsupported(w, "custom cluster endpoints")
		return
	}

	ep, err := store.ModifyDBClusterEndpoint(r.Context(), r.Form.Get("DBClusterEndpointIdentifier"), rdsdriver.ModifyClusterEndpointInput{
		EndpointType:    r.Form.Get("EndpointType"),
		StaticMembers:   awsquery.ListStrings(r.Form, "StaticMembers.member"),
		ExcludedMembers: awsquery.ListStrings(r.Form, "ExcludedMembers.member"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBClusterEndpointResponse{
		Xmlns:    Namespace,
		Result:   toClusterEndpointXML(ep),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBClusterEndpoint(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterEndpointsCap()
	if !ok {
		writeUnsupported(w, "custom cluster endpoints")
		return
	}

	ep, err := store.DeleteDBClusterEndpoint(r.Context(), r.Form.Get("DBClusterEndpointIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBClusterEndpointResponse{
		Xmlns:    Namespace,
		Result:   toClusterEndpointXML(ep),
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// ---- failover handler ----

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) failoverDBCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterFailoverCap()
	if !ok {
		writeUnsupported(w, "cluster failover")
		return
	}

	cluster, err := store.FailoverDBCluster(r.Context(),
		r.Form.Get("DBClusterIdentifier"), r.Form.Get("TargetDBInstanceIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, failoverDBClusterResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors removeRoleFromDBCluster by design.
func (h *Handler) addRoleToDBCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterRolesCap()
	if !ok {
		writeUnsupported(w, "cluster IAM role association")
		return
	}

	err := store.AddRoleToDBCluster(r.Context(),
		r.Form.Get("DBClusterIdentifier"), r.Form.Get("RoleArn"), r.Form.Get("FeatureName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addRoleToDBClusterResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors addRoleToDBCluster by design.
func (h *Handler) removeRoleFromDBCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.clusterRolesCap()
	if !ok {
		writeUnsupported(w, "cluster IAM role association")
		return
	}

	err := store.RemoveRoleFromDBCluster(r.Context(),
		r.Form.Get("DBClusterIdentifier"), r.Form.Get("RoleArn"), r.Form.Get("FeatureName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeRoleFromDBClusterResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// ---- global cluster handlers ----

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) createGlobalCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.globalClustersCap()
	if !ok {
		writeUnsupported(w, "global clusters")
		return
	}

	gc, err := store.CreateGlobalCluster(r.Context(), rdsdriver.GlobalClusterConfig{
		ID:                r.Form.Get("GlobalClusterIdentifier"),
		Engine:            r.Form.Get("Engine"),
		EngineVersion:     r.Form.Get("EngineVersion"),
		SourceDBClusterID: r.Form.Get("SourceDBClusterIdentifier"),
		Tags:              parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createGlobalClusterResponse{
		Xmlns:    Namespace,
		Result:   globalClusterResult{GlobalCluster: toGlobalClusterXML(gc)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) describeGlobalClusters(w http.ResponseWriter, r *http.Request) {
	store, ok := h.globalClustersCap()
	if !ok {
		writeUnsupported(w, "global clusters")
		return
	}

	var ids []string
	if id := r.Form.Get("GlobalClusterIdentifier"); id != "" {
		ids = []string{id}
	}

	clusters, err := store.DescribeGlobalClusters(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]globalClusterXML, 0, len(clusters))
	for i := range clusters {
		out = append(out, toGlobalClusterXML(&clusters[i]))
	}

	awsquery.WriteXMLResponse(w, describeGlobalClustersResponse{
		Xmlns:    Namespace,
		Result:   globalClusterList{GlobalClusters: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) modifyGlobalCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.globalClustersCap()
	if !ok {
		writeUnsupported(w, "global clusters")
		return
	}

	gc, err := store.ModifyGlobalCluster(r.Context(),
		r.Form.Get("GlobalClusterIdentifier"),
		r.Form.Get("NewGlobalClusterIdentifier"),
		r.Form.Get("EngineVersion"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyGlobalClusterResponse{
		Xmlns:    Namespace,
		Result:   globalClusterResult{GlobalCluster: toGlobalClusterXML(gc)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteGlobalCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.globalClustersCap()
	if !ok {
		writeUnsupported(w, "global clusters")
		return
	}

	gc, err := store.DeleteGlobalCluster(r.Context(), r.Form.Get("GlobalClusterIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteGlobalClusterResponse{
		Xmlns:    Namespace,
		Result:   globalClusterResult{GlobalCluster: toGlobalClusterXML(gc)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) removeFromGlobalCluster(w http.ResponseWriter, r *http.Request) {
	store, ok := h.globalClustersCap()
	if !ok {
		writeUnsupported(w, "global clusters")
		return
	}

	gc, err := store.RemoveFromGlobalCluster(r.Context(),
		r.Form.Get("GlobalClusterIdentifier"), r.Form.Get("DbClusterIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removeFromGlobalClusterResponse{
		Xmlns:    Namespace,
		Result:   globalClusterResult{GlobalCluster: toGlobalClusterXML(gc)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
