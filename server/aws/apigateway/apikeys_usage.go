package apigateway

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// serveAPIKeys handles /apikeys (GET list, POST create) and /apikeys/{id}.
func (h *Handler) serveAPIKeys(w http.ResponseWriter, r *http.Request, rest []string) {
	svc, ok := h.ag.(driver.APIKeys)
	if !ok || len(rest) > 1 {
		notImplemented(w)
		return
	}

	if len(rest) == 1 {
		id := rest[0]

		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.APIKey, error) {
				return svc.UpdateAPIKey(r.Context(), id, ops)
			},
			func() (*driver.APIKey, error) { return svc.GetAPIKey(r.Context(), id, queryBool(r, "includeValue")) },
			func() error { return svc.DeleteAPIKey(r.Context(), id) },
			toAPIKeyResponse,
		)

		return
	}

	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()

		serveListOf(w, r, func(page driver.PageInput) ([]driver.APIKey, string, error) {
			res, err := svc.GetAPIKeys(r.Context(), &driver.GetAPIKeysInput{
				NameQuery: q.Get("name"), CustomerID: q.Get("customerId"), IncludeValues: queryBool(r, "includeValues"), PageInput: page,
			})
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toAPIKeyResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *createAPIKeyRequest) (*driver.APIKey, error) {
			in := &driver.CreateAPIKeyInput{
				Name: req.Name, Description: req.Description, Enabled: req.Enabled, Value: req.Value,
				CustomerID: req.CustomerID, Tags: req.Tags,
			}
			for _, sk := range req.StageKeys {
				in.StageKeys = append(in.StageKeys, driver.StageKey{RestAPIID: sk.RestAPIID, StageName: sk.StageName})
			}

			return svc.CreateAPIKey(r.Context(), in)
		}, toAPIKeyResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveUsagePlans handles /usageplans, /usageplans/{id}, /usageplans/{id}/keys,
// /usageplans/{id}/keys/{keyId} and /usageplans/{id}/usage.
//
//nolint:dupl // the same item/collection router shape as the sibling resource families by design
func (h *Handler) serveUsagePlans(w http.ResponseWriter, r *http.Request, rest []string) {
	svc, ok := h.ag.(driver.UsagePlans)
	if !ok {
		notImplemented(w)
		return
	}

	const (
		planOnly = 1
		planSub  = 2
		planItem = 3
	)

	switch len(rest) {
	case 0:
		h.serveUsagePlanCollection(w, r, svc)
	case planOnly:
		id := rest[0]

		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.UsagePlan, error) {
				return svc.UpdateUsagePlan(r.Context(), id, ops)
			},
			func() (*driver.UsagePlan, error) { return svc.GetUsagePlan(r.Context(), id) },
			func() error { return svc.DeleteUsagePlan(r.Context(), id) },
			toUsagePlanResponse,
		)
	case planSub:
		h.serveUsagePlanSub(w, r, svc, rest[0], rest[1])
	case planItem:
		h.serveUsagePlanKey(w, r, svc, rest)
	default:
		notImplemented(w)
	}
}

func (*Handler) serveUsagePlanCollection(w http.ResponseWriter, r *http.Request, svc driver.UsagePlans) {
	switch r.Method {
	case http.MethodGet:
		keyID := r.URL.Query().Get("keyId")

		serveListOf(w, r, func(page driver.PageInput) ([]driver.UsagePlan, string, error) {
			res, err := svc.GetUsagePlans(r.Context(), keyID, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toUsagePlanResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *createUsagePlanRequest) (*driver.UsagePlan, error) {
			in := &driver.CreateUsagePlanInput{
				Name: req.Name, Description: req.Description, APIStages: planStagesFromWire(req.APIStages), Tags: req.Tags,
			}
			if req.Throttle != nil {
				in.Throttle = &driver.ThrottleSettings{BurstLimit: req.Throttle.BurstLimit, RateLimit: req.Throttle.RateLimit}
			}

			if req.Quota != nil {
				in.Quota = &driver.QuotaSettings{Limit: req.Quota.Limit, Offset: req.Quota.Offset, Period: req.Quota.Period}
			}

			return svc.CreateUsagePlan(r.Context(), in)
		}, toUsagePlanResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveUsagePlanSub handles /usageplans/{id}/keys and /usageplans/{id}/usage.
func (*Handler) serveUsagePlanSub(w http.ResponseWriter, r *http.Request, svc driver.UsagePlans, planID, sub string) {
	switch {
	case sub == segKeys && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")

		serveListOf(w, r, func(page driver.PageInput) ([]driver.UsagePlanKey, string, error) {
			res, err := svc.GetUsagePlanKeys(r.Context(), planID, name, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toUsagePlanKeyResponse)
	case sub == segKeys && r.Method == http.MethodPost:
		serveCreateOf(w, r, func(req *createUsagePlanKeyRequest) (*driver.UsagePlanKey, error) {
			return svc.CreateUsagePlanKey(r.Context(), planID, req.KeyID, req.KeyType)
		}, toUsagePlanKeyResponse)
	case sub == "usage" && r.Method == http.MethodGet:
		page, ok := pageInput(w, r)
		if !ok {
			return
		}

		q := r.URL.Query()

		u, err := svc.GetUsage(r.Context(), &driver.GetUsageInput{
			UsagePlanID: planID, KeyID: q.Get("keyId"), StartDate: q.Get("startDate"), EndDate: q.Get("endDate"), PageInput: page,
		})
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toUsageResponse(u))
	default:
		writeMethodNotAllowed(w)
	}
}

func toUsagePlanKeyResponse(k *driver.UsagePlanKey) usagePlanKeyResponse {
	return usagePlanKeyResponse{ID: k.ID, Type: k.Type, Value: k.Value, Name: k.Name}
}

// serveUsagePlanKey handles /usageplans/{id}/keys/{keyId}: GET, DELETE.
func (*Handler) serveUsagePlanKey(w http.ResponseWriter, r *http.Request, svc driver.UsagePlans, rest []string) {
	if rest[1] != segKeys {
		notImplemented(w)
		return
	}

	planID, keyID := rest[0], rest[2]

	switch r.Method {
	case http.MethodGet:
		k, err := svc.GetUsagePlanKey(r.Context(), planID, keyID)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toUsagePlanKeyResponse(k))
	case http.MethodDelete:
		if err := svc.DeleteUsagePlanKey(r.Context(), planID, keyID); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	default:
		writeMethodNotAllowed(w)
	}
}
