package apprunner

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerVpcConnectorRoutes wires the VPC connector operations.
func (h *Handler) registerVpcConnectorRoutes() {
	h.routes["CreateVpcConnector"] = h.createVpcConnector
	h.routes["DescribeVpcConnector"] = h.describeVpcConnector
	h.routes["DeleteVpcConnector"] = h.deleteVpcConnector
	h.routes["ListVpcConnectors"] = h.listVpcConnectors
}

// vpcConnectorJSON is the wire shape of a VPC connector resource.
type vpcConnectorJSON struct {
	VpcConnectorName     string   `json:"VpcConnectorName"`
	VpcConnectorArn      string   `json:"VpcConnectorArn"`
	VpcConnectorRevision int32    `json:"VpcConnectorRevision"`
	Subnets              []string `json:"Subnets,omitempty"`
	SecurityGroups       []string `json:"SecurityGroups,omitempty"`
	Status               string   `json:"Status"`
	CreatedAt            any      `json:"CreatedAt,omitempty"`
	DeletedAt            any      `json:"DeletedAt,omitempty"`
}

func vpcConnectorToWire(c *driver.VpcConnector) vpcConnectorJSON {
	return vpcConnectorJSON{
		VpcConnectorName:     c.VpcConnectorName,
		VpcConnectorArn:      c.VpcConnectorArn,
		VpcConnectorRevision: c.VpcConnectorRevision,
		Subnets:              c.Subnets,
		SecurityGroups:       c.SecurityGroups,
		Status:               c.Status,
		CreatedAt:            epochSeconds(c.CreatedAt),
		DeletedAt:            epochSeconds(c.DeletedAt),
	}
}

type createVpcConnectorRequest struct {
	VpcConnectorName string    `json:"VpcConnectorName"`
	Subnets          []string  `json:"Subnets"`
	SecurityGroups   []string  `json:"SecurityGroups"`
	Tags             []tagJSON `json:"Tags"`
}

type vpcConnectorResponse struct {
	VpcConnector vpcConnectorJSON `json:"VpcConnector"`
}

func (h *Handler) createVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createVpcConnectorRequest) (any, error) {
		conn, err := h.apprunner.CreateVpcConnector(ctx, &driver.CreateVpcConnectorInput{
			VpcConnectorName: req.VpcConnectorName,
			Subnets:          req.Subnets,
			SecurityGroups:   req.SecurityGroups,
			Tags:             tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(conn)}, nil
	})
}

type vpcConnectorArnRequest struct {
	VpcConnectorArn string `json:"VpcConnectorArn"`
}

func (h *Handler) describeVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcConnectorArnRequest) (any, error) {
		conn, err := h.apprunner.DescribeVpcConnector(ctx, req.VpcConnectorArn)
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(conn)}, nil
	})
}

func (h *Handler) deleteVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcConnectorArnRequest) (any, error) {
		conn, err := h.apprunner.DeleteVpcConnector(ctx, req.VpcConnectorArn)
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(conn)}, nil
	})
}

type listVpcConnectorsRequest struct {
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listVpcConnectorsResponse struct {
	VpcConnectors []vpcConnectorJSON `json:"VpcConnectors"`
	NextToken     string             `json:"NextToken,omitempty"`
}

func (h *Handler) listVpcConnectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listVpcConnectorsRequest) (any, error) {
		conns, next, err := h.apprunner.ListVpcConnectors(ctx, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return listVpcConnectorsResponse{
			VpcConnectors: mapWire(conns, vpcConnectorToWire),
			NextToken:     next,
		}, nil
	})
}
