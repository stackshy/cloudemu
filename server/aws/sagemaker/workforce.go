package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

type wireCognitoMember struct {
	ClientID  string `json:"ClientId"`
	UserGroup string `json:"UserGroup"`
	UserPool  string `json:"UserPool"`
}

type wireOidcMember struct {
	Groups []string `json:"Groups"`
}

type wireMember struct {
	CognitoMemberDefinition *wireCognitoMember `json:"CognitoMemberDefinition,omitempty"`
	OidcMemberDefinition    *wireOidcMember    `json:"OidcMemberDefinition,omitempty"`
}

func toMembers(in []wireMember) []driver.MemberDefinition {
	out := make([]driver.MemberDefinition, 0, len(in))

	for _, m := range in {
		var d driver.MemberDefinition

		if c := m.CognitoMemberDefinition; c != nil {
			d.CognitoClientID, d.CognitoUserPool, d.CognitoUserGroup = c.ClientID, c.UserPool, c.UserGroup
		}

		if o := m.OidcMemberDefinition; o != nil {
			d.OIDCGroups = o.Groups
		}

		out = append(out, d)
	}

	return out
}

func fromMembers(in []driver.MemberDefinition) []wireMember {
	out := make([]wireMember, 0, len(in))

	for _, d := range in {
		if len(d.OIDCGroups) > 0 {
			out = append(out, wireMember{OidcMemberDefinition: &wireOidcMember{Groups: d.OIDCGroups}})

			continue
		}

		out = append(out, wireMember{CognitoMemberDefinition: &wireCognitoMember{
			ClientID: d.CognitoClientID, UserGroup: d.CognitoUserGroup, UserPool: d.CognitoUserPool,
		}})
	}

	return out
}

// wireWorkerAccess is the JSON shape of WorkerAccessConfiguration.
type wireWorkerAccess struct {
	S3Presign struct {
		IamPolicyConstraints struct {
			SourceIP    string `json:"SourceIp,omitempty"`
			VpcSourceIP string `json:"VpcSourceIp,omitempty"`
		} `json:"IamPolicyConstraints"`
	} `json:"S3Presign"`
}

func workteamToWire(t *driver.Workteam) map[string]any {
	out := map[string]any{
		"WorkteamName":      t.Name,
		"WorkteamArn":       t.ARN,
		"MemberDefinitions": fromMembers(t.Members),
		"Description":       t.Description,
		"CreateDate":        epoch(t.CreateDate),
		"LastUpdatedDate":   epoch(t.LastUpdatedDate),
		"SubDomain":         t.SubDomain,
		"ProductListingIds": t.ProductListingIDs,
		"WorkforceArn":      t.WorkforceARN,
	}

	if t.NotificationTopicARN != "" {
		out["NotificationConfiguration"] = map[string]any{"NotificationTopicArn": t.NotificationTopicARN}
	}

	if t.S3PresignSourceIP != "" || t.S3PresignVpcSourceIP != "" {
		var a wireWorkerAccess

		a.S3Presign.IamPolicyConstraints.SourceIP = t.S3PresignSourceIP
		a.S3Presign.IamPolicyConstraints.VpcSourceIP = t.S3PresignVpcSourceIP
		out["WorkerAccessConfiguration"] = a
	}

	return out
}

func (h *Handler) createWorkteam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkteamName              string       `json:"WorkteamName"`
		Description               string       `json:"Description"`
		WorkforceName             string       `json:"WorkforceName"`
		MemberDefinitions         []wireMember `json:"MemberDefinitions"`
		NotificationConfiguration *struct {
			NotificationTopicArn string `json:"NotificationTopicArn"`
		} `json:"NotificationConfiguration"`
		WorkerAccessConfiguration *wireWorkerAccess `json:"WorkerAccessConfiguration"`
		Tags                      []wireTag         `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.WorkteamSpec{
		Name: req.WorkteamName, Description: req.Description, WorkforceName: req.WorkforceName,
		Members: toMembers(req.MemberDefinitions), Tags: toTags(req.Tags),
	}

	if n := req.NotificationConfiguration; n != nil {
		spec.NotificationTopicARN = n.NotificationTopicArn
	}

	if a := req.WorkerAccessConfiguration; a != nil {
		spec.S3PresignSourceIP = a.S3Presign.IamPolicyConstraints.SourceIP
		spec.S3PresignVpcSourceIP = a.S3Presign.IamPolicyConstraints.VpcSourceIP
	}

	t, err := h.svc.CreateWorkteam(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"WorkteamArn": t.ARN})
}

func (h *Handler) describeWorkteam(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "WorkteamName")
	if !ok {
		return
	}

	t, err := h.svc.DescribeWorkteam(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Workteam": workteamToWire(t)})
}

func (h *Handler) listWorkteams(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListWorkteams(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	items := make([]map[string]any, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, workteamToWire(&page.Items[i]))
	}

	resp := map[string]any{"Workteams": items}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) updateWorkteam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkteamName              string        `json:"WorkteamName"`
		Description               *string       `json:"Description"`
		MemberDefinitions         *[]wireMember `json:"MemberDefinitions"`
		NotificationConfiguration *struct {
			NotificationTopicArn string `json:"NotificationTopicArn"`
		} `json:"NotificationConfiguration"`
		WorkerAccessConfiguration *wireWorkerAccess `json:"WorkerAccessConfiguration"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up := driver.WorkteamUpdate{Name: req.WorkteamName, Description: req.Description}

	if req.MemberDefinitions != nil {
		up.Members = toMembers(*req.MemberDefinitions)
		if up.Members == nil {
			up.Members = []driver.MemberDefinition{}
		}
	}

	if n := req.NotificationConfiguration; n != nil {
		up.NotificationTopicARN = &n.NotificationTopicArn
	}

	if a := req.WorkerAccessConfiguration; a != nil {
		up.S3PresignSourceIP = &a.S3Presign.IamPolicyConstraints.SourceIP
		up.S3PresignVpcSourceIP = &a.S3Presign.IamPolicyConstraints.VpcSourceIP
	}

	t, err := h.svc.UpdateWorkteam(r.Context(), up)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Workteam": workteamToWire(t)})
}

func (h *Handler) deleteWorkteam(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "WorkteamName")
	if !ok {
		return
	}

	deleted, err := h.svc.DeleteWorkteam(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Success": deleted})
}

func (h *Handler) describeSubscribedWorkteam(w http.ResponseWriter, r *http.Request) {
	arn, ok := decodeName1(w, r, "WorkteamArn")
	if !ok {
		return
	}

	t, err := h.svc.DescribeSubscribedWorkteam(r.Context(), arn)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"SubscribedWorkteam": map[string]any{
		"WorkteamArn": t.ARN, "ListingId": t.ListingID, "MarketplaceTitle": t.MarketplaceTitle,
		"MarketplaceDescription": t.MarketplaceDescription, "SellerName": t.SellerName,
	}})
}

func (h *Handler) listSubscribedWorkteams(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListSubscribedWorkteams(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "SubscribedWorkteams", page, func(t *driver.SubscribedWorkteam) map[string]any {
		return map[string]any{"WorkteamArn": t.ARN, "ListingId": t.ListingID, "MarketplaceTitle": t.MarketplaceTitle, "SellerName": t.SellerName}
	})
}

func (h *Handler) listLabelingJobsForWorkteam(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListLabelingJobsForWorkteam(r.Context(), req.WorkteamArn, req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "LabelingJobSummaryList", page, func(j *driver.LabelingJobForWorkteam) map[string]any {
		return map[string]any{
			"LabelingJobName":                   j.JobName,
			"JobReferenceCode":                  j.JobReferenceCode,
			"WorkRequesterAccountId":            j.WorkRequesterAccountID,
			"NumberOfHumanWorkersPerDataObject": j.NumberOfHumanWorkers,
			"CreationTime":                      epoch(j.CreationTime),
			"LabelCounters":                     map[string]any{"HumanLabeled": j.HumanLabeled, "PendingHuman": j.PendingHuman, "Total": j.Total},
		}
	})
}

// --- Workforce ---

type wireCognitoConfig struct {
	ClientID string `json:"ClientId"`
	UserPool string `json:"UserPool"`
}

type wireOidcConfig struct {
	AuthorizationEndpoint            string            `json:"AuthorizationEndpoint"`
	ClientID                         string            `json:"ClientId"`
	ClientSecret                     string            `json:"ClientSecret,omitempty"`
	Issuer                           string            `json:"Issuer"`
	JwksURI                          string            `json:"JwksUri"`
	LogoutEndpoint                   string            `json:"LogoutEndpoint"`
	TokenEndpoint                    string            `json:"TokenEndpoint"`
	UserInfoEndpoint                 string            `json:"UserInfoEndpoint"`
	Scope                            string            `json:"Scope,omitempty"`
	AuthenticationRequestExtraParams map[string]string `json:"AuthenticationRequestExtraParams,omitempty"`
}

type wireWorkforceVPC struct {
	VpcID            string   `json:"VpcId"`
	SecurityGroupIDs []string `json:"SecurityGroupIds"`
	Subnets          []string `json:"Subnets"`
	VpcEndpointID    string   `json:"VpcEndpointId,omitempty"`
}

func toOIDC(o *wireOidcConfig) *driver.OIDCConfig {
	if o == nil {
		return nil
	}

	return &driver.OIDCConfig{
		AuthorizationEndpoint: o.AuthorizationEndpoint, ClientID: o.ClientID, ClientSecret: o.ClientSecret, Issuer: o.Issuer,
		JwksURI: o.JwksURI, LogoutEndpoint: o.LogoutEndpoint, TokenEndpoint: o.TokenEndpoint, UserInfoEndpoint: o.UserInfoEndpoint,
		Scope: o.Scope, ExtraParams: o.AuthenticationRequestExtraParams,
	}
}

func toWorkforceVPC(v *wireWorkforceVPC) *driver.WorkforceVPC {
	if v == nil {
		return nil
	}

	return &driver.WorkforceVPC{VPCID: v.VpcID, SecurityGroupIDs: v.SecurityGroupIDs, Subnets: v.Subnets}
}

func workforceToWire(f *driver.Workforce) map[string]any {
	out := map[string]any{
		"WorkforceName":   f.Name,
		"WorkforceArn":    f.ARN,
		"Status":          f.Status,
		"SubDomain":       f.SubDomain,
		"CreateDate":      epoch(f.CreateDate),
		"LastUpdatedDate": epoch(f.LastUpdatedDate),
		"IpAddressType":   f.IPAddressType,
	}
	setIf(out, "FailureReason", f.FailureReason)

	if f.Cognito != nil {
		out["CognitoConfig"] = wireCognitoConfig{ClientID: f.Cognito.ClientID, UserPool: f.Cognito.UserPool}
	}

	if o := f.OIDC; o != nil { // OidcConfigForResponse never carries the client secret
		out["OidcConfig"] = wireOidcConfig{
			AuthorizationEndpoint: o.AuthorizationEndpoint, ClientID: o.ClientID, Issuer: o.Issuer, JwksURI: o.JwksURI,
			LogoutEndpoint: o.LogoutEndpoint, TokenEndpoint: o.TokenEndpoint, UserInfoEndpoint: o.UserInfoEndpoint,
			Scope: o.Scope, AuthenticationRequestExtraParams: o.ExtraParams,
		}
	}

	if len(f.SourceCIDRs) > 0 {
		out["SourceIpConfig"] = map[string]any{"Cidrs": f.SourceCIDRs}
	}

	if v := f.VPC; v != nil {
		out["WorkforceVpcConfig"] = wireWorkforceVPC{VpcID: v.VPCID, SecurityGroupIDs: v.SecurityGroupIDs, Subnets: v.Subnets, VpcEndpointID: v.VPCEndpointID}
	}

	return out
}

func (h *Handler) createWorkforce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkforceName  string             `json:"WorkforceName"`
		CognitoConfig  *wireCognitoConfig `json:"CognitoConfig"`
		OidcConfig     *wireOidcConfig    `json:"OidcConfig"`
		SourceIPConfig *struct {
			Cidrs []string `json:"Cidrs"`
		} `json:"SourceIpConfig"`
		IPAddressType      string            `json:"IpAddressType"`
		WorkforceVpcConfig *wireWorkforceVPC `json:"WorkforceVpcConfig"`
		Tags               []wireTag         `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	spec := driver.WorkforceSpec{
		Name: req.WorkforceName, OIDC: toOIDC(req.OidcConfig), IPAddressType: req.IPAddressType,
		VPC: toWorkforceVPC(req.WorkforceVpcConfig), Tags: toTags(req.Tags),
	}

	if c := req.CognitoConfig; c != nil {
		spec.Cognito = &driver.CognitoConfig{ClientID: c.ClientID, UserPool: c.UserPool}
	}

	if s := req.SourceIPConfig; s != nil {
		spec.SourceCIDRs = s.Cidrs
	}

	f, err := h.svc.CreateWorkforce(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"WorkforceArn": f.ARN})
}

func (h *Handler) describeWorkforce(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "WorkforceName")
	if !ok {
		return
	}

	f, err := h.svc.DescribeWorkforce(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Workforce": workforceToWire(f)})
}

func (h *Handler) listWorkforces(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListWorkforces(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	items := make([]map[string]any, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, workforceToWire(&page.Items[i]))
	}

	resp := map[string]any{"Workforces": items}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) updateWorkforce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkforceName  string `json:"WorkforceName"`
		SourceIPConfig *struct {
			Cidrs []string `json:"Cidrs"`
		} `json:"SourceIpConfig"`
		OidcConfig         *wireOidcConfig   `json:"OidcConfig"`
		IPAddressType      *string           `json:"IpAddressType"`
		WorkforceVpcConfig *wireWorkforceVPC `json:"WorkforceVpcConfig"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up := driver.WorkforceUpdate{
		Name: req.WorkforceName, OIDC: toOIDC(req.OidcConfig), IPAddressType: req.IPAddressType, VPC: toWorkforceVPC(req.WorkforceVpcConfig),
	}

	if s := req.SourceIPConfig; s != nil {
		up.SourceCIDRs = s.Cidrs
		if up.SourceCIDRs == nil {
			up.SourceCIDRs = []string{}
		}
	}

	f, err := h.svc.UpdateWorkforce(r.Context(), up)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Workforce": workforceToWire(f)})
}

func (h *Handler) deleteWorkforce(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "WorkforceName", h.svc.DeleteWorkforce)
}
