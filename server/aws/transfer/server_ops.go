package transfer

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

type createServerRequest struct {
	Domain                  string               `json:"Domain"`
	EndpointType            string               `json:"EndpointType"`
	EndpointDetails         *endpointDetailsJSON `json:"EndpointDetails"`
	HostKey                 string               `json:"HostKey"`
	IdentityProviderType    string               `json:"IdentityProviderType"`
	IdentityProviderDetails map[string]string    `json:"IdentityProviderDetails"`
	LoggingRole             string               `json:"LoggingRole"`
	Protocols               []string             `json:"Protocols"`
	SecurityPolicyName      string               `json:"SecurityPolicyName"`
	Certificate             string               `json:"Certificate"`
	Tags                    []tagJSON            `json:"Tags"`
}

type createServerResponse struct {
	ServerID string `json:"ServerId"`
}

func (h *Handler) createServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createServerRequest) (any, error) {
		s := driver.Server{
			Domain:                  req.Domain,
			EndpointType:            req.EndpointType,
			EndpointDetails:         endpointDetailsFromWire(req.EndpointDetails),
			IdentityProviderType:    req.IdentityProviderType,
			IdentityProviderDetails: req.IdentityProviderDetails,
			LoggingRole:             req.LoggingRole,
			Protocols:               req.Protocols,
			SecurityPolicyName:      req.SecurityPolicyName,
			Certificate:             req.Certificate,
			Tags:                    tagsToMap(req.Tags),
		}

		id, err := h.transfer.CreateServer(ctx, s)
		if err != nil {
			return nil, err
		}

		return createServerResponse{ServerID: id}, nil
	})
}

type describeServerRequest struct {
	ServerID string `json:"ServerId"`
}

type describeServerResponse struct {
	Server describedServerJSON `json:"Server"`
}

func (h *Handler) describeServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeServerRequest) (any, error) {
		s, err := h.transfer.DescribeServer(ctx, req.ServerID)
		if err != nil {
			return nil, err
		}

		return describeServerResponse{Server: serverToWire(s)}, nil
	})
}

type updateServerRequest struct {
	ServerID                string               `json:"ServerId"`
	EndpointType            *string              `json:"EndpointType"`
	EndpointDetails         *endpointDetailsJSON `json:"EndpointDetails"`
	IdentityProviderDetails map[string]string    `json:"IdentityProviderDetails"`
	LoggingRole             *string              `json:"LoggingRole"`
	Protocols               []string             `json:"Protocols"`
	SecurityPolicyName      *string              `json:"SecurityPolicyName"`
	Certificate             *string              `json:"Certificate"`
}

type updateServerResponse struct {
	ServerID string `json:"ServerId"`
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateServerRequest) (any, error) {
		upd := driver.ServerUpdate{
			EndpointType:            req.EndpointType,
			EndpointDetails:         endpointDetailsFromWire(req.EndpointDetails),
			IdentityProviderDetails: req.IdentityProviderDetails,
			LoggingRole:             req.LoggingRole,
			Protocols:               req.Protocols,
			SecurityPolicyName:      req.SecurityPolicyName,
			Certificate:             req.Certificate,
		}
		if err := h.transfer.UpdateServer(ctx, req.ServerID, upd); err != nil {
			return nil, err
		}

		return updateServerResponse{ServerID: req.ServerID}, nil
	})
}

type deleteServerRequest struct {
	ServerID string `json:"ServerId"`
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteServerRequest) (any, error) {
		if err := h.transfer.DeleteServer(ctx, req.ServerID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listServersRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listServersResponse struct {
	Servers   []listedServerJSON `json:"Servers"`
	NextToken string             `json:"NextToken,omitempty"`
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listServersRequest) (any, error) {
		servers, next, err := h.transfer.ListServers(ctx,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]listedServerJSON, 0, len(servers))
		for i := range servers {
			out = append(out, listedServerToWire(&servers[i]))
		}

		return listServersResponse{Servers: out, NextToken: next}, nil
	})
}

type startStopServerRequest struct {
	ServerID string `json:"ServerId"`
}

func (h *Handler) startServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startStopServerRequest) (any, error) {
		if err := h.transfer.StartServer(ctx, req.ServerID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopServer(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startStopServerRequest) (any, error) {
		if err := h.transfer.StopServer(ctx, req.ServerID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
