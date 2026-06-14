package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) routeStudio(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateDomain":
		h.createDomain(w, r)
	case "DescribeDomain":
		h.describeDomain(w, r)
	case "ListDomains":
		h.listDomains(w, r)
	case "DeleteDomain":
		stopByName(w, r, "DomainId", h.svc.DeleteDomain)
	case "CreateUserProfile":
		h.createUserProfile(w, r)
	case "DescribeUserProfile":
		h.describeUserProfile(w, r)
	case "ListUserProfiles":
		h.listUserProfiles(w, r)
	case "DeleteUserProfile":
		h.deleteUserProfile(w, r)
	default:
		return h.routeStudioSpacesApps(w, r, op)
	}

	return true
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) routeStudioSpacesApps(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateSpace":
		h.createSpace(w, r)
	case "DescribeSpace":
		h.describeSpace(w, r)
	case "ListSpaces":
		h.listSpaces(w, r)
	case "DeleteSpace":
		h.deleteSpace(w, r)
	case "CreateApp":
		h.createApp(w, r)
	case "DescribeApp":
		h.describeApp(w, r)
	case "ListApps":
		h.listApps(w, r)
	case "DeleteApp":
		h.deleteApp(w, r)
	default:
		return false
	}

	return true
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) createSpace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID  string    `json:"DomainId"`
		SpaceName string    `json:"SpaceName"`
		Tags      []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	sp, err := h.svc.CreateSpace(r.Context(), driver.SpaceSpec{
		DomainID:  req.DomainID,
		SpaceName: req.SpaceName,
		Tags:      toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"SpaceArn": sp.SpaceARN})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) describeSpace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID  string `json:"DomainId"`
		SpaceName string `json:"SpaceName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	sp, err := h.svc.DescribeSpace(r.Context(), req.DomainID, req.SpaceName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"DomainId":     sp.DomainID,
		"SpaceName":    sp.SpaceName,
		"SpaceArn":     sp.SpaceARN,
		"Status":       sp.Status,
		"CreationTime": epoch(sp.CreationTime),
	})
}

func (h *Handler) listSpaces(w http.ResponseWriter, r *http.Request) {
	domainID, ok := decodeName1(w, r, "DomainIdEquals")
	if !ok {
		return
	}

	spaces, err := h.svc.ListSpaces(r.Context(), domainID)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Spaces", spaces, func(s *driver.Space) map[string]any {
		return map[string]any{
			"DomainId":     s.DomainID,
			"SpaceName":    s.SpaceName,
			"Status":       s.Status,
			"CreationTime": epoch(s.CreationTime),
		}
	})
}

func (h *Handler) deleteSpace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID  string `json:"DomainId"`
		SpaceName string `json:"SpaceName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteSpace(r.Context(), req.DomainID, req.SpaceName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) createDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainName          string   `json:"DomainName"`
		AuthMode            string   `json:"AuthMode"`
		VpcID               string   `json:"VpcId"`
		SubnetIDs           []string `json:"SubnetIds"`
		DefaultUserSettings struct {
			ExecutionRole string `json:"ExecutionRole"`
		} `json:"DefaultUserSettings"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d, err := h.svc.CreateDomain(r.Context(), driver.DomainSpec{
		DomainName:       req.DomainName,
		AuthMode:         req.AuthMode,
		VPCID:            req.VpcID,
		SubnetIDs:        req.SubnetIDs,
		ExecutionRoleARN: req.DefaultUserSettings.ExecutionRole,
		Tags:             toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"DomainArn": d.DomainARN, "DomainId": d.DomainID, "Url": d.URL})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) describeDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := decodeName1(w, r, "DomainId")
	if !ok {
		return
	}

	d, err := h.svc.DescribeDomain(r.Context(), id)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"DomainId":     d.DomainID,
		"DomainArn":    d.DomainARN,
		"DomainName":   d.DomainName,
		"Status":       d.Status,
		"AuthMode":     d.AuthMode,
		"Url":          d.URL,
		"CreationTime": epoch(d.CreationTime),
	})
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := h.svc.ListDomains(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Domains", domains, func(d *driver.Domain) map[string]any {
		return map[string]any{
			"DomainId":     d.DomainID,
			"DomainArn":    d.DomainARN,
			"DomainName":   d.DomainName,
			"Status":       d.Status,
			"CreationTime": epoch(d.CreationTime),
		}
	})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) createUserProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
		UserSettings    struct {
			ExecutionRole string `json:"ExecutionRole"`
		} `json:"UserSettings"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up, err := h.svc.CreateUserProfile(r.Context(), driver.UserProfileSpec{
		DomainID:         req.DomainID,
		UserProfileName:  req.UserProfileName,
		ExecutionRoleARN: req.UserSettings.ExecutionRole,
		Tags:             toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"UserProfileArn": up.UserProfileARN})
}

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) describeUserProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up, err := h.svc.DescribeUserProfile(r.Context(), req.DomainID, req.UserProfileName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"DomainId":        up.DomainID,
		"UserProfileName": up.UserProfileName,
		"UserProfileArn":  up.UserProfileARN,
		"Status":          up.Status,
		"CreationTime":    epoch(up.CreationTime),
	})
}

func (h *Handler) listUserProfiles(w http.ResponseWriter, r *http.Request) {
	domainID, ok := decodeName1(w, r, "DomainIdEquals")
	if !ok {
		return
	}

	ups, err := h.svc.ListUserProfiles(r.Context(), domainID)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "UserProfiles", ups, func(u *driver.UserProfile) map[string]any {
		return map[string]any{
			"DomainId":        u.DomainID,
			"UserProfileName": u.UserProfileName,
			"Status":          u.Status,
			"CreationTime":    epoch(u.CreationTime),
		}
	})
}

func (h *Handler) deleteUserProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteUserProfile(r.Context(), req.DomainID, req.UserProfileName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func appSpecFromReq(domainID, userProfile, space, appType, appName string) driver.AppSpec {
	return driver.AppSpec{
		DomainID:        domainID,
		UserProfileName: userProfile,
		SpaceName:       space,
		AppType:         appType,
		AppName:         appName,
	}
}

func (h *Handler) createApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
		SpaceName       string `json:"SpaceName"`
		AppType         string `json:"AppType"`
		AppName         string `json:"AppName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	app, err := h.svc.CreateApp(r.Context(),
		appSpecFromReq(req.DomainID, req.UserProfileName, req.SpaceName, req.AppType, req.AppName))
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"AppArn": app.AppARN})
}

func (h *Handler) describeApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
		SpaceName       string `json:"SpaceName"`
		AppType         string `json:"AppType"`
		AppName         string `json:"AppName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	app, err := h.svc.DescribeApp(r.Context(),
		appSpecFromReq(req.DomainID, req.UserProfileName, req.SpaceName, req.AppType, req.AppName))
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"AppArn":       app.AppARN,
		"AppName":      app.AppName,
		"AppType":      app.AppType,
		"DomainId":     app.DomainID,
		"Status":       app.Status,
		"CreationTime": epoch(app.CreationTime),
	})
}

func (h *Handler) listApps(w http.ResponseWriter, r *http.Request) {
	domainID, ok := decodeName1(w, r, "DomainIdEquals")
	if !ok {
		return
	}

	apps, err := h.svc.ListApps(r.Context(), domainID)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "Apps", apps, func(a *driver.App) map[string]any {
		return map[string]any{
			"AppName":      a.AppName,
			"AppType":      a.AppType,
			"DomainId":     a.DomainID,
			"Status":       a.Status,
			"CreationTime": epoch(a.CreationTime),
		}
	})
}

func (h *Handler) deleteApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainID        string `json:"DomainId"`
		UserProfileName string `json:"UserProfileName"`
		SpaceName       string `json:"SpaceName"`
		AppType         string `json:"AppType"`
		AppName         string `json:"AppName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteApp(r.Context(),
		appSpecFromReq(req.DomainID, req.UserProfileName, req.SpaceName, req.AppType, req.AppName)); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}
