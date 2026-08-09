package cloudtrail

import (
	"context"
	"net/http"
)

type putEventSelectorsRequest struct {
	TrailName              string                      `json:"TrailName"`
	EventSelectors         []eventSelectorJSON         `json:"EventSelectors"`
	AdvancedEventSelectors []advancedEventSelectorJSON `json:"AdvancedEventSelectors"`
}

type eventSelectorsResponse struct {
	TrailARN               string                      `json:"TrailARN,omitempty"`
	EventSelectors         []eventSelectorJSON         `json:"EventSelectors,omitempty"`
	AdvancedEventSelectors []advancedEventSelectorJSON `json:"AdvancedEventSelectors,omitempty"`
}

func (h *Handler) putEventSelectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putEventSelectorsRequest) (any, error) {
		arn, sel, adv, err := h.ct.PutEventSelectors(ctx, req.TrailName,
			eventSelectorsFromWire(req.EventSelectors), advSelectorsFromWire(req.AdvancedEventSelectors))
		if err != nil {
			return nil, err
		}

		return eventSelectorsResponse{
			TrailARN: arn, EventSelectors: eventSelectorsToWire(sel), AdvancedEventSelectors: advSelectorsToWire(adv),
		}, nil
	})
}

func (h *Handler) getEventSelectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		TrailName string `json:"TrailName"`
	},
	) (any, error) {
		arn, sel, adv, err := h.ct.GetEventSelectors(ctx, req.TrailName)
		if err != nil {
			return nil, err
		}

		return eventSelectorsResponse{
			TrailARN: arn, EventSelectors: eventSelectorsToWire(sel), AdvancedEventSelectors: advSelectorsToWire(adv),
		}, nil
	})
}

type putInsightSelectorsRequest struct {
	TrailName        string                `json:"TrailName"`
	EventDataStore   string                `json:"EventDataStore"`
	InsightSelectors []insightSelectorJSON `json:"InsightSelectors"`
}

type insightSelectorsResponse struct {
	TrailARN          string                `json:"TrailARN,omitempty"`
	EventDataStoreArn string                `json:"EventDataStoreArn,omitempty"`
	InsightSelectors  []insightSelectorJSON `json:"InsightSelectors,omitempty"`
}

func (h *Handler) putInsightSelectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putInsightSelectorsRequest) (any, error) {
		arn, edsARN, sel, err := h.ct.PutInsightSelectors(ctx, req.TrailName, req.EventDataStore,
			insightSelectorsFromWire(req.InsightSelectors))
		if err != nil {
			return nil, err
		}

		return insightSelectorsResponse{
			TrailARN: arn, EventDataStoreArn: edsARN, InsightSelectors: insightSelectorsToWire(sel),
		}, nil
	})
}

func (h *Handler) getInsightSelectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		TrailName      string `json:"TrailName"`
		EventDataStore string `json:"EventDataStore"`
	},
	) (any, error) {
		arn, edsARN, sel, err := h.ct.GetInsightSelectors(ctx, req.TrailName, req.EventDataStore)
		if err != nil {
			return nil, err
		}

		return insightSelectorsResponse{
			TrailARN: arn, EventDataStoreArn: edsARN, InsightSelectors: insightSelectorsToWire(sel),
		}, nil
	})
}
