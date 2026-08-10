package cloudtrail

import (
	"context"
	"net/http"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

type createEDSRequest struct {
	Name                         string                      `json:"Name"`
	BillingMode                  string                      `json:"BillingMode"`
	RetentionPeriod              *int32                      `json:"RetentionPeriod"`
	MultiRegionEnabled           *bool                       `json:"MultiRegionEnabled"`
	OrganizationEnabled          *bool                       `json:"OrganizationEnabled"`
	TerminationProtectionEnabled *bool                       `json:"TerminationProtectionEnabled"`
	StartIngestion               *bool                       `json:"StartIngestion"`
	KmsKeyID                     string                      `json:"KmsKeyId"`
	AdvancedEventSelectors       []advancedEventSelectorJSON `json:"AdvancedEventSelectors"`
	TagsList                     []tag                       `json:"TagsList"`
}

type updateEDSRequest struct {
	EventDataStore               string                      `json:"EventDataStore"`
	Name                         *string                     `json:"Name"`
	BillingMode                  *string                     `json:"BillingMode"`
	RetentionPeriod              *int32                      `json:"RetentionPeriod"`
	MultiRegionEnabled           *bool                       `json:"MultiRegionEnabled"`
	OrganizationEnabled          *bool                       `json:"OrganizationEnabled"`
	TerminationProtectionEnabled *bool                       `json:"TerminationProtectionEnabled"`
	KmsKeyID                     *string                     `json:"KmsKeyId"`
	AdvancedEventSelectors       []advancedEventSelectorJSON `json:"AdvancedEventSelectors"`
}

type edsARNRequest struct {
	EventDataStore string `json:"EventDataStore"`
}

func (h *Handler) createEventDataStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createEDSRequest) (any, error) {
		e, err := h.ct.CreateEventDataStore(ctx, ctdriver.CreateEventDataStoreInput{
			Name:                         req.Name,
			BillingMode:                  req.BillingMode,
			RetentionPeriod:              req.RetentionPeriod,
			MultiRegionEnabled:           req.MultiRegionEnabled,
			OrganizationEnabled:          req.OrganizationEnabled,
			TerminationProtectionEnabled: req.TerminationProtectionEnabled,
			StartIngestion:               req.StartIngestion,
			KMSKeyID:                     req.KmsKeyID,
			AdvancedEventSelectors:       advSelectorsFromWire(req.AdvancedEventSelectors),
			Tags:                         tagsToMap(req.TagsList),
		})
		if err != nil {
			return nil, err
		}

		return edsToWire(e), nil
	})
}

func (h *Handler) getEventDataStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *edsARNRequest) (any, error) {
		e, err := h.ct.GetEventDataStore(ctx, req.EventDataStore)
		if err != nil {
			return nil, err
		}

		return edsToWire(e), nil
	})
}

func (h *Handler) updateEventDataStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateEDSRequest) (any, error) {
		e, err := h.ct.UpdateEventDataStore(ctx, ctdriver.UpdateEventDataStoreInput{
			ARN:                          req.EventDataStore,
			Name:                         req.Name,
			BillingMode:                  req.BillingMode,
			RetentionPeriod:              req.RetentionPeriod,
			MultiRegionEnabled:           req.MultiRegionEnabled,
			OrganizationEnabled:          req.OrganizationEnabled,
			TerminationProtectionEnabled: req.TerminationProtectionEnabled,
			KMSKeyID:                     req.KmsKeyID,
			AdvancedEventSelectors:       advSelectorsFromWire(req.AdvancedEventSelectors),
		})
		if err != nil {
			return nil, err
		}

		return edsToWire(e), nil
	})
}

func (h *Handler) deleteEventDataStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *edsARNRequest) (any, error) {
		if err := h.ct.DeleteEventDataStore(ctx, req.EventDataStore); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) restoreEventDataStore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *edsARNRequest) (any, error) {
		e, err := h.ct.RestoreEventDataStore(ctx, req.EventDataStore)
		if err != nil {
			return nil, err
		}

		return edsToWire(e), nil
	})
}

//nolint:dupl // list-op shape mirrors listDashboards but returns a distinct wire type.
func (h *Handler) listEventDataStores(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listRequest) (any, error) {
		stores, next, err := h.ct.ListEventDataStores(ctx, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		list := make([]edsJSON, 0, len(stores))
		for i := range stores {
			list = append(list, edsToWire(&stores[i]))
		}

		return struct {
			EventDataStores []edsJSON `json:"EventDataStores"`
			NextToken       string    `json:"NextToken,omitempty"`
		}{EventDataStores: list, NextToken: next}, nil
	})
}

func (h *Handler) startEDSIngestion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *edsARNRequest) (any, error) {
		if err := h.ct.StartEventDataStoreIngestion(ctx, req.EventDataStore); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopEDSIngestion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *edsARNRequest) (any, error) {
		if err := h.ct.StopEventDataStoreIngestion(ctx, req.EventDataStore); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
