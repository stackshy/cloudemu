package sagemaker

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// wireFeatureDef is the JSON shape of a feature definition.
type wireFeatureDef struct {
	FeatureName string `json:"FeatureName"`
	FeatureType string `json:"FeatureType"`
}

// wireFeatureValue is the JSON shape of an online-store record entry.
type wireFeatureValue struct {
	FeatureName   string `json:"FeatureName"`
	ValueAsString string `json:"ValueAsString"`
}

func (h *Handler) routeFeatureStore(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateFeatureGroup":
		h.createFeatureGroup(w, r)
	case "DescribeFeatureGroup":
		h.describeFeatureGroup(w, r)
	case "ListFeatureGroups":
		h.listFeatureGroups(w, r)
	case "DeleteFeatureGroup":
		stopByName(w, r, "FeatureGroupName", h.svc.DeleteFeatureGroup)
	default:
		return false
	}

	return true
}

func (h *Handler) createFeatureGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FeatureGroupName            string           `json:"FeatureGroupName"`
		RecordIdentifierFeatureName string           `json:"RecordIdentifierFeatureName"`
		EventTimeFeatureName        string           `json:"EventTimeFeatureName"`
		FeatureDefinitions          []wireFeatureDef `json:"FeatureDefinitions"`
		RoleArn                     string           `json:"RoleArn"`
		OnlineStoreConfig           *struct{}        `json:"OnlineStoreConfig"`
		OfflineStoreConfig          struct {
			S3StorageConfig struct {
				S3URI string `json:"S3Uri"`
			} `json:"S3StorageConfig"`
		} `json:"OfflineStoreConfig"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	defs := make([]driver.FeatureDefinition, 0, len(req.FeatureDefinitions))
	for _, d := range req.FeatureDefinitions {
		defs = append(defs, driver.FeatureDefinition{Name: d.FeatureName, Type: d.FeatureType})
	}

	fg, err := h.svc.CreateFeatureGroup(r.Context(), driver.FeatureGroupSpec{
		GroupName:            req.FeatureGroupName,
		RecordIdentifierName: req.RecordIdentifierFeatureName,
		EventTimeFeatureName: req.EventTimeFeatureName,
		Features:             defs,
		OnlineStoreEnabled:   req.OnlineStoreConfig != nil,
		OfflineStoreS3URI:    req.OfflineStoreConfig.S3StorageConfig.S3URI,
		RoleARN:              req.RoleArn,
		Tags:                 toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"FeatureGroupArn": fg.GroupARN})
}

func (h *Handler) describeFeatureGroup(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "FeatureGroupName")
	if !ok {
		return
	}

	fg, err := h.svc.DescribeFeatureGroup(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	defs := make([]wireFeatureDef, 0, len(fg.Features))
	for _, d := range fg.Features {
		defs = append(defs, wireFeatureDef{FeatureName: d.Name, FeatureType: d.Type})
	}

	wire.WriteJSON(w, map[string]any{
		"FeatureGroupName":            fg.GroupName,
		"FeatureGroupArn":             fg.GroupARN,
		"RecordIdentifierFeatureName": fg.RecordIdentifierName,
		"EventTimeFeatureName":        fg.EventTimeFeatureName,
		"FeatureDefinitions":          defs,
		"FeatureGroupStatus":          fg.Status,
		"CreationTime":                epoch(fg.CreationTime),
	})
}

func (h *Handler) listFeatureGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.ListFeatureGroups(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "FeatureGroupSummaries", groups, func(g *driver.FeatureGroup) map[string]any {
		return map[string]any{
			"FeatureGroupName":   g.GroupName,
			"FeatureGroupArn":    g.GroupARN,
			"FeatureGroupStatus": g.Status,
			"CreationTime":       epoch(g.CreationTime),
		}
	})
}

// serveFeatureStoreRuntime handles the sagemaker-featurestore-runtime REST API.
// The smithy binding is POST/GET/DELETE /FeatureGroup/{FeatureGroupName} for
// PutRecord/GetRecord/DeleteRecord respectively.
func (h *Handler) serveFeatureStoreRuntime(w http.ResponseWriter, r *http.Request) {
	group := decodeName(strings.TrimPrefix(r.URL.Path, "/FeatureGroup/"))

	switch r.Method {
	case http.MethodPut:
		h.putRecord(w, r, group)
	case http.MethodGet:
		h.getRecord(w, r, group)
	case http.MethodDelete:
		h.deleteRecord(w, r, group)
	default:
		wire.WriteJSONError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putRecord(w http.ResponseWriter, r *http.Request, group string) {
	var req struct {
		Record []wireFeatureValue `json:"Record"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rec := make([]driver.FeatureValue, 0, len(req.Record))
	for _, fv := range req.Record {
		rec = append(rec, driver.FeatureValue{Name: fv.FeatureName, Value: fv.ValueAsString})
	}

	if err := h.svc.PutRecord(r.Context(), group, rec); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) getRecord(w http.ResponseWriter, r *http.Request, group string) {
	recordID := r.URL.Query().Get("RecordIdentifierValueAsString")

	rec, err := h.svc.GetRecord(r.Context(), group, recordID)
	if err != nil {
		// FeatureStore GetRecord returns 200 with no Record when absent.
		if cerrors.IsNotFound(err) {
			wire.WriteJSON(w, map[string]any{})

			return
		}

		writeDriverError(w, err)

		return
	}

	out := make([]wireFeatureValue, 0, len(rec))
	for _, fv := range rec {
		out = append(out, wireFeatureValue{FeatureName: fv.Name, ValueAsString: fv.Value})
	}

	wire.WriteJSON(w, map[string]any{"Record": out})
}

func (h *Handler) deleteRecord(w http.ResponseWriter, r *http.Request, group string) {
	recordID := r.URL.Query().Get("RecordIdentifierValueAsString")

	if err := h.svc.DeleteRecord(r.Context(), group, recordID); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}
