package globalaccelerator

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// registerListenerRoutes wires the listener operations.
func (h *Handler) registerListenerRoutes() {
	h.routes["CreateListener"] = h.createListener
	h.routes["DescribeListener"] = h.describeListener
	h.routes["UpdateListener"] = h.updateListener
	h.routes["DeleteListener"] = h.deleteListener
	h.routes["ListListeners"] = h.listListeners
}

type createListenerRequest struct {
	AcceleratorArn   string          `json:"AcceleratorArn"`
	PortRanges       []portRangeJSON `json:"PortRanges"`
	Protocol         string          `json:"Protocol"`
	ClientAffinity   string          `json:"ClientAffinity"`
	IdempotencyToken string          `json:"IdempotencyToken"`
}

type listenerResponse struct {
	Listener listenerJSON `json:"Listener"`
}

func (h *Handler) createListener(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createListenerRequest) (any, error) {
		l, err := h.ga.CreateListener(ctx, &driver.CreateListenerInput{
			AcceleratorArn:   req.AcceleratorArn,
			PortRanges:       portRangesFromWire(req.PortRanges),
			Protocol:         req.Protocol,
			ClientAffinity:   req.ClientAffinity,
			IdempotencyToken: req.IdempotencyToken,
		})
		if err != nil {
			return nil, err
		}

		return listenerResponse{Listener: listenerToWire(l)}, nil
	})
}

type describeListenerRequest struct {
	ListenerArn string `json:"ListenerArn"`
}

func (h *Handler) describeListener(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeListenerRequest) (any, error) {
		l, err := h.ga.DescribeListener(ctx, req.ListenerArn)
		if err != nil {
			return nil, err
		}

		return listenerResponse{Listener: listenerToWire(l)}, nil
	})
}

type updateListenerRequest struct {
	ListenerArn    string          `json:"ListenerArn"`
	PortRanges     []portRangeJSON `json:"PortRanges"`
	Protocol       *string         `json:"Protocol"`
	ClientAffinity *string         `json:"ClientAffinity"`
}

func (h *Handler) updateListener(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateListenerRequest) (any, error) {
		l, err := h.ga.UpdateListener(ctx, &driver.UpdateListenerInput{
			ListenerArn:    req.ListenerArn,
			PortRanges:     portRangesFromWire(req.PortRanges),
			Protocol:       req.Protocol,
			ClientAffinity: req.ClientAffinity,
		})
		if err != nil {
			return nil, err
		}

		return listenerResponse{Listener: listenerToWire(l)}, nil
	})
}

type deleteListenerRequest struct {
	ListenerArn string `json:"ListenerArn"`
}

func (h *Handler) deleteListener(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteListenerRequest) (any, error) {
		if err := h.ga.DeleteListener(ctx, req.ListenerArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listListenersRequest struct {
	AcceleratorArn string `json:"AcceleratorArn"`
	MaxResults     int32  `json:"MaxResults"`
	NextToken      string `json:"NextToken"`
}

type listListenersResponse struct {
	Listeners []listenerJSON `json:"Listeners"`
	NextToken string         `json:"NextToken,omitempty"`
}

func (h *Handler) listListeners(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listListenersRequest) (any, error) {
		ls, next, err := h.ga.ListListeners(ctx, req.AcceleratorArn,
			driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return listListenersResponse{
			Listeners: toWireList(ls, listenerToWire),
			NextToken: next,
		}, nil
	})
}
