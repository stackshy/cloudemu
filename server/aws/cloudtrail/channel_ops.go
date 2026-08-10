package cloudtrail

import (
	"context"
	"net/http"
)

// listRequest is the common pagination request for CloudTrail list ops.
type listRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type createChannelRequest struct {
	Name         string            `json:"Name"`
	Source       string            `json:"Source"`
	Destinations []destinationJSON `json:"Destinations"`
	Tags         []tag             `json:"Tags"`
}

func (h *Handler) createChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createChannelRequest) (any, error) {
		c, err := h.ct.CreateChannel(ctx, req.Name, req.Source,
			destinationsFromWire(req.Destinations), tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return struct {
			ChannelArn string `json:"ChannelArn"`
			Name       string `json:"Name"`
		}{ChannelArn: c.ARN, Name: c.Name}, nil
	})
}

func (h *Handler) getChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		Channel string `json:"Channel"`
	},
	) (any, error) {
		c, err := h.ct.GetChannel(ctx, req.Channel)
		if err != nil {
			return nil, err
		}

		return channelToWire(c), nil
	})
}

func (h *Handler) updateChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		Channel      string            `json:"Channel"`
		Name         string            `json:"Name"`
		Destinations []destinationJSON `json:"Destinations"`
	},
	) (any, error) {
		c, err := h.ct.UpdateChannel(ctx, req.Channel, req.Name, destinationsFromWire(req.Destinations))
		if err != nil {
			return nil, err
		}

		return struct {
			ChannelArn   string            `json:"ChannelArn"`
			Name         string            `json:"Name"`
			Source       string            `json:"Source,omitempty"`
			Destinations []destinationJSON `json:"Destinations,omitempty"`
		}{ChannelArn: c.ARN, Name: c.Name, Source: c.Source, Destinations: destinationsToWire(c.Destinations)}, nil
	})
}

func (h *Handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		Channel string `json:"Channel"`
	},
	) (any, error) {
		if err := h.ct.DeleteChannel(ctx, req.Channel); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listChannels(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listRequest) (any, error) {
		channels, next, err := h.ct.ListChannels(ctx, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		type chItem struct {
			ChannelArn string `json:"ChannelArn,omitempty"`
			Name       string `json:"Name,omitempty"`
		}

		list := make([]chItem, 0, len(channels))
		for i := range channels {
			list = append(list, chItem{ChannelArn: channels[i].ARN, Name: channels[i].Name})
		}

		return struct {
			Channels  []chItem `json:"Channels"`
			NextToken string   `json:"NextToken,omitempty"`
		}{Channels: list, NextToken: next}, nil
	})
}
