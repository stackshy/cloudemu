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

type userAuthConfigInfoXML struct {
	AuthScheme  string `xml:"AuthScheme,omitempty"`
	SecretArn   string `xml:"SecretArn,omitempty"`
	IAMAuth     string `xml:"IAMAuth,omitempty"`
	Description string `xml:"Description,omitempty"`
}

type dbProxyXML struct {
	DBProxyName         string                  `xml:"DBProxyName"`
	DBProxyArn          string                  `xml:"DBProxyArn"`
	Status              string                  `xml:"Status"`
	EngineFamily        string                  `xml:"EngineFamily,omitempty"`
	RoleArn             string                  `xml:"RoleArn,omitempty"`
	Endpoint            string                  `xml:"Endpoint,omitempty"`
	RequireTLS          bool                    `xml:"RequireTLS"`
	IdleClientTimeout   int                     `xml:"IdleClientTimeout,omitempty"`
	DebugLogging        bool                    `xml:"DebugLogging"`
	VpcSubnetIds        []string                `xml:"VpcSubnetIds>member,omitempty"`
	VpcSecurityGroupIds []string                `xml:"VpcSecurityGroupIds>member,omitempty"`
	Auth                []userAuthConfigInfoXML `xml:"Auth>member,omitempty"`
	CreatedDate         string                  `xml:"CreatedDate,omitempty"`
}

type proxyTargetXML struct {
	Type          string `xml:"Type,omitempty"`
	RdsResourceID string `xml:"RdsResourceId,omitempty"`
	Endpoint      string `xml:"Endpoint,omitempty"`
	Port          int    `xml:"Port,omitempty"`
}

type proxyTargetGroupXML struct {
	DBProxyName     string `xml:"DBProxyName"`
	TargetGroupName string `xml:"TargetGroupName"`
	TargetGroupArn  string `xml:"TargetGroupArn,omitempty"`
	IsDefault       bool   `xml:"IsDefault"`
	Status          string `xml:"Status,omitempty"`
}

type dbProxyResult struct {
	DBProxy dbProxyXML `xml:"DBProxy"`
}

type createDBProxyResponse struct {
	XMLName  xml.Name         `xml:"CreateDBProxyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbProxyResult    `xml:"CreateDBProxyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type modifyDBProxyResponse struct {
	XMLName  xml.Name         `xml:"ModifyDBProxyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbProxyResult    `xml:"ModifyDBProxyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteDBProxyResponse struct {
	XMLName  xml.Name         `xml:"DeleteDBProxyResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbProxyResult    `xml:"DeleteDBProxyResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeDBProxiesResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBProxiesResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbProxiesList    `xml:"DescribeDBProxiesResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type dbProxiesList struct {
	DBProxies []dbProxyXML `xml:"DBProxies>member"`
}

type registerDBProxyTargetsResponse struct {
	XMLName  xml.Name              `xml:"RegisterDBProxyTargetsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   proxyTargetsRegResult `xml:"RegisterDBProxyTargetsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type proxyTargetsRegResult struct {
	DBProxyTargets []proxyTargetXML `xml:"DBProxyTargets>member"`
}

type deregisterDBProxyTargetsResponse struct {
	XMLName  xml.Name         `xml:"DeregisterDBProxyTargetsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   struct{}         `xml:"DeregisterDBProxyTargetsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeDBProxyTargetsResponse struct {
	XMLName  xml.Name         `xml:"DescribeDBProxyTargetsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   proxyTargetsList `xml:"DescribeDBProxyTargetsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type proxyTargetsList struct {
	Targets []proxyTargetXML `xml:"Targets>member"`
}

type describeDBProxyTargetGroupsResponse struct {
	XMLName  xml.Name              `xml:"DescribeDBProxyTargetGroupsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   proxyTargetGroupsList `xml:"DescribeDBProxyTargetGroupsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type proxyTargetGroupsList struct {
	TargetGroups []proxyTargetGroupXML `xml:"TargetGroups>member"`
}

// ---- helpers ----

func (h *Handler) dbProxiesCap() (rdsdriver.DBProxies, bool) {
	p, ok := h.db.(rdsdriver.DBProxies)

	return p, ok
}

// parseProxyAuth reads Auth.member.N.{AuthScheme,SecretArn,IAMAuth,Description,ClientPasswordAuthType}.
func parseProxyAuth(form url.Values) []rdsdriver.ProxyAuth {
	indices := awsquery.CollectIndices(form, "Auth.member")
	if len(indices) == 0 {
		return nil
	}

	out := make([]rdsdriver.ProxyAuth, 0, len(indices))

	for _, n := range indices {
		base := "Auth.member." + strconv.Itoa(n)
		out = append(out, rdsdriver.ProxyAuth{
			AuthScheme:             form.Get(base + ".AuthScheme"),
			SecretARN:              form.Get(base + ".SecretArn"),
			IAMAuth:                form.Get(base + ".IAMAuth"),
			Description:            form.Get(base + ".Description"),
			ClientPasswordAuthType: form.Get(base + ".ClientPasswordAuthType"),
		})
	}

	return out
}

func toProxyXML(p *rdsdriver.DBProxy) dbProxyXML {
	x := dbProxyXML{
		DBProxyName:         p.Name,
		DBProxyArn:          p.ARN,
		Status:              p.Status,
		EngineFamily:        p.EngineFamily,
		RoleArn:             p.RoleARN,
		Endpoint:            p.Endpoint,
		RequireTLS:          p.RequireTLS,
		IdleClientTimeout:   p.IdleClientTimeout,
		DebugLogging:        p.DebugLogging,
		VpcSubnetIds:        p.VPCSubnetIDs,
		VpcSecurityGroupIds: p.VPCSecurityGroupIDs,
		CreatedDate:         p.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}

	for _, a := range p.Auth {
		x.Auth = append(x.Auth, userAuthConfigInfoXML{
			AuthScheme:  a.AuthScheme,
			SecretArn:   a.SecretARN,
			IAMAuth:     a.IAMAuth,
			Description: a.Description,
		})
	}

	return x
}

func toProxyTargetsXML(targets []rdsdriver.ProxyTarget) []proxyTargetXML {
	out := make([]proxyTargetXML, 0, len(targets))
	for _, t := range targets {
		out = append(out, proxyTargetXML{
			Type:          t.Type,
			RdsResourceID: t.RDSResourceID,
			Endpoint:      t.Endpoint,
			Port:          t.Port,
		})
	}

	return out
}

// ---- handlers ----

func (h *Handler) createDBProxy(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	proxy, err := store.CreateDBProxy(r.Context(), rdsdriver.DBProxyConfig{
		Name:                r.Form.Get("DBProxyName"),
		EngineFamily:        r.Form.Get("EngineFamily"),
		RoleARN:             r.Form.Get("RoleArn"),
		VPCSubnetIDs:        awsquery.ListStrings(r.Form, "VpcSubnetIds.member"),
		VPCSecurityGroupIDs: awsquery.ListStrings(r.Form, "VpcSecurityGroupIds.member"),
		RequireTLS:          formBool(r.Form.Get("RequireTLS")),
		IdleClientTimeout:   formInt(r.Form.Get("IdleClientTimeout")),
		DebugLogging:        formBool(r.Form.Get("DebugLogging")),
		Auth:                parseProxyAuth(r.Form),
		Tags:                parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBProxyResponse{
		Xmlns:    Namespace,
		Result:   dbProxyResult{DBProxy: toProxyXML(proxy)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBProxies(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	var names []string
	if n := r.Form.Get("DBProxyName"); n != "" {
		names = []string{n}
	}

	proxies, err := store.DescribeDBProxies(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]dbProxyXML, 0, len(proxies))
	for i := range proxies {
		out = append(out, toProxyXML(&proxies[i]))
	}

	awsquery.WriteXMLResponse(w, describeDBProxiesResponse{
		Xmlns:    Namespace,
		Result:   dbProxiesList{DBProxies: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) modifyDBProxy(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	proxy, err := store.ModifyDBProxy(r.Context(), r.Form.Get("DBProxyName"), rdsdriver.ModifyDBProxyInput{
		RequireTLS:        optBool(r.Form, "RequireTLS"),
		IdleClientTimeout: optInt(r.Form, "IdleClientTimeout"),
		DebugLogging:      optBool(r.Form, "DebugLogging"),
		RoleARN:           r.Form.Get("RoleArn"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyDBProxyResponse{
		Xmlns:    Namespace,
		Result:   dbProxyResult{DBProxy: toProxyXML(proxy)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteDBProxy(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	proxy, err := store.DeleteDBProxy(r.Context(), r.Form.Get("DBProxyName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteDBProxyResponse{
		Xmlns:    Namespace,
		Result:   dbProxyResult{DBProxy: toProxyXML(proxy)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) registerDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	targets, err := store.RegisterDBProxyTargets(r.Context(),
		r.Form.Get("DBProxyName"), r.Form.Get("TargetGroupName"),
		awsquery.ListStrings(r.Form, "DBInstanceIdentifiers.member"),
		awsquery.ListStrings(r.Form, "DBClusterIdentifiers.member"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, registerDBProxyTargetsResponse{
		Xmlns:    Namespace,
		Result:   proxyTargetsRegResult{DBProxyTargets: toProxyTargetsXML(targets)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deregisterDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	if err := store.DeregisterDBProxyTargets(r.Context(),
		r.Form.Get("DBProxyName"), r.Form.Get("TargetGroupName"),
		awsquery.ListStrings(r.Form, "DBInstanceIdentifiers.member"),
		awsquery.ListStrings(r.Form, "DBClusterIdentifiers.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deregisterDBProxyTargetsResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBProxyTargets(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	targets, err := store.DescribeDBProxyTargets(r.Context(),
		r.Form.Get("DBProxyName"), r.Form.Get("TargetGroupName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeDBProxyTargetsResponse{
		Xmlns:    Namespace,
		Result:   proxyTargetsList{Targets: toProxyTargetsXML(targets)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeDBProxyTargetGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.dbProxiesCap()
	if !ok {
		writeUnsupported(w, "DB proxies")
		return
	}

	groups, err := store.DescribeDBProxyTargetGroups(r.Context(), r.Form.Get("DBProxyName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]proxyTargetGroupXML, 0, len(groups))
	for _, g := range groups {
		out = append(out, proxyTargetGroupXML{
			DBProxyName:     g.ProxyName,
			TargetGroupName: g.Name,
			TargetGroupArn:  g.ARN,
			IsDefault:       g.IsDefault,
			Status:          "available",
		})
	}

	awsquery.WriteXMLResponse(w, describeDBProxyTargetGroupsResponse{
		Xmlns:    Namespace,
		Result:   proxyTargetGroupsList{TargetGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// optBool / optInt return a pointer only when the form field is present, so a
// Modify call leaves unspecified fields unchanged.
func optBool(form url.Values, key string) *bool {
	if _, ok := form[key]; !ok {
		return nil
	}

	v := formBool(form.Get(key))

	return &v
}

func optInt(form url.Values, key string) *int {
	if _, ok := form[key]; !ok {
		return nil
	}

	v := formInt(form.Get(key))

	return &v
}
