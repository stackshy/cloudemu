package configservice

import (
	"context"
	"net/http"
)

type putChannelReq struct {
	DeliveryChannel *deliveryChannelJSON `json:"DeliveryChannel"`
}

func (h *Handler) putDeliveryChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putChannelReq) (any, error) {
		if req.DeliveryChannel == nil {
			return nil, invalidRequest("DeliveryChannel is required")
		}

		if err := h.cfg.PutDeliveryChannel(ctx, req.DeliveryChannel.toDriver()); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type channelNamesReq struct {
	DeliveryChannelNames []string `json:"DeliveryChannelNames"`
}

type describeChannelsResp struct {
	DeliveryChannels []deliveryChannelJSON `json:"DeliveryChannels"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeDeliveryChannels(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *channelNamesReq) (any, error) {
		chs, err := h.cfg.DescribeDeliveryChannels(ctx, req.DeliveryChannelNames)
		if err != nil {
			return nil, err
		}

		out := make([]deliveryChannelJSON, 0, len(chs))
		for i := range chs {
			out = append(out, channelToWire(&chs[i]))
		}

		return describeChannelsResp{DeliveryChannels: out}, nil
	})
}

type describeChannelStatusResp struct {
	DeliveryChannelsStatus []channelStatusJSON `json:"DeliveryChannelsStatus"`
}

func (h *Handler) describeDeliveryChannelStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *channelNamesReq) (any, error) {
		chs, err := h.cfg.DescribeDeliveryChannelStatus(ctx, req.DeliveryChannelNames)
		if err != nil {
			return nil, err
		}

		out := make([]channelStatusJSON, 0, len(chs))
		for i := range chs {
			out = append(out, channelStatusJSON{Name: chs[i].Name})
		}

		return describeChannelStatusResp{DeliveryChannelsStatus: out}, nil
	})
}

type channelNameReq struct {
	DeliveryChannelName string `json:"DeliveryChannelName"`
}

func (h *Handler) deleteDeliveryChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *channelNameReq) (any, error) {
		if err := h.cfg.DeleteDeliveryChannel(ctx, req.DeliveryChannelName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deliverSnapshotResp struct {
	ConfigSnapshotID string `json:"configSnapshotId,omitempty"`
}

func (h *Handler) deliverConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *channelNameReq) (any, error) {
		id, err := h.cfg.DeliverConfigSnapshot(ctx, req.DeliveryChannelName)
		if err != nil {
			return nil, err
		}

		return deliverSnapshotResp{ConfigSnapshotID: id}, nil
	})
}
