package apprunner

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerConnectionRoutes wires the connection operations.
func (h *Handler) registerConnectionRoutes() {
	h.routes["CreateConnection"] = h.createConnection
	h.routes["DeleteConnection"] = h.deleteConnection
	h.routes["ListConnections"] = h.listConnections
}

// connectionJSON is the wire shape of a connection resource, shared by the
// create/delete responses and the list summary.
type connectionJSON struct {
	ConnectionName string `json:"ConnectionName"`
	ConnectionArn  string `json:"ConnectionArn"`
	ProviderType   string `json:"ProviderType"`
	Status         string `json:"Status"`
	CreatedAt      any    `json:"CreatedAt,omitempty"`
}

func connectionToWire(c *driver.Connection) connectionJSON {
	return connectionJSON{
		ConnectionName: c.ConnectionName,
		ConnectionArn:  c.ConnectionArn,
		ProviderType:   c.ProviderType,
		Status:         c.Status,
		CreatedAt:      epochSeconds(c.CreatedAt),
	}
}

type createConnectionRequest struct {
	ConnectionName string    `json:"ConnectionName"`
	ProviderType   string    `json:"ProviderType"`
	Tags           []tagJSON `json:"Tags"`
}

type connectionResponse struct {
	Connection connectionJSON `json:"Connection"`
}

func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createConnectionRequest) (any, error) {
		conn, err := h.apprunner.CreateConnection(ctx, &driver.CreateConnectionInput{
			ConnectionName: req.ConnectionName,
			ProviderType:   req.ProviderType,
			Tags:           tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return connectionResponse{Connection: connectionToWire(conn)}, nil
	})
}

type deleteConnectionRequest struct {
	ConnectionArn string `json:"ConnectionArn"`
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteConnectionRequest) (any, error) {
		conn, err := h.apprunner.DeleteConnection(ctx, req.ConnectionArn)
		if err != nil {
			return nil, err
		}

		return connectionResponse{Connection: connectionToWire(conn)}, nil
	})
}

type listConnectionsRequest struct {
	ConnectionName string `json:"ConnectionName"`
	MaxResults     int32  `json:"MaxResults"`
	NextToken      string `json:"NextToken"`
}

type listConnectionsResponse struct {
	ConnectionSummaryList []connectionJSON `json:"ConnectionSummaryList"`
	NextToken             string           `json:"NextToken,omitempty"`
}

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listConnectionsRequest) (any, error) {
		conns, next, err := h.apprunner.ListConnections(ctx, req.ConnectionName, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return listConnectionsResponse{
			ConnectionSummaryList: mapWire(conns, connectionToWire),
			NextToken:             next,
		}, nil
	})
}
