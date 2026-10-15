package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerVpcIngressRoutes wires the VPC ingress connection operations.
func (h *Handler) registerVpcIngressRoutes(d driver.VpcIngressConnections) {
	h.routes["CreateVpcIngressConnection"] = handle(h,
		func(ctx context.Context, req *createVpcIngressConnectionRequest) (vpcIngressConnectionResponse, error) {
			c, err := d.CreateVpcIngressConnection(ctx, &driver.CreateVpcIngressConnectionInput{
				VpcIngressConnectionName: req.VpcIngressConnectionName, ServiceArn: req.ServiceArn,
				IngressVpcConfiguration: ingressConfigFromWire(req.IngressVpcConfiguration), Tags: tagsFromWire(req.Tags),
			})
			if err != nil {
				return vpcIngressConnectionResponse{}, err
			}

			return vpcIngressConnectionResponse{VpcIngressConnection: ingressToWire(c)}, nil
		})
	h.routes["DescribeVpcIngressConnection"] = handle(h,
		func(ctx context.Context, req *vpcIngressConnectionRef) (vpcIngressConnectionResponse, error) {
			c, err := d.DescribeVpcIngressConnection(ctx, req.VpcIngressConnectionArn)
			if err != nil {
				return vpcIngressConnectionResponse{}, err
			}

			return vpcIngressConnectionResponse{VpcIngressConnection: ingressToWire(c)}, nil
		})
	h.routes["UpdateVpcIngressConnection"] = handle(h,
		func(ctx context.Context, req *updateVpcIngressConnectionRequest) (vpcIngressConnectionResponse, error) {
			c, err := d.UpdateVpcIngressConnection(ctx, req.VpcIngressConnectionArn, ingressConfigFromWire(req.IngressVpcConfiguration))
			if err != nil {
				return vpcIngressConnectionResponse{}, err
			}

			return vpcIngressConnectionResponse{VpcIngressConnection: ingressToWire(c)}, nil
		})
	h.routes["DeleteVpcIngressConnection"] = handle(h,
		func(ctx context.Context, req *vpcIngressConnectionRef) (vpcIngressConnectionResponse, error) {
			c, err := d.DeleteVpcIngressConnection(ctx, req.VpcIngressConnectionArn)
			if err != nil {
				return vpcIngressConnectionResponse{}, err
			}

			return vpcIngressConnectionResponse{VpcIngressConnection: ingressToWire(c)}, nil
		})
	h.routes["ListVpcIngressConnections"] = handle(h,
		func(ctx context.Context, req *listVpcIngressConnectionsRequest) (listVpcIngressConnectionsResponse, error) {
			filter := driver.ListVpcIngressConnectionsFilter{}
			if req.Filter != nil {
				filter = driver.ListVpcIngressConnectionsFilter{ServiceArn: req.Filter.ServiceArn, VpcEndpointID: req.Filter.VpcEndpointID}
			}

			items, next, err := d.ListVpcIngressConnections(ctx, filter, pageFromWire(req.MaxResults, req.NextToken))
			if err != nil {
				return listVpcIngressConnectionsResponse{}, err
			}

			return listVpcIngressConnectionsResponse{
				VpcIngressConnectionSummaryList: mapWire(items, func(i driver.VpcIngressConnectionSummary) vpcIngressSummaryJSON {
					return vpcIngressSummaryJSON{VpcIngressConnectionArn: i.VpcIngressConnectionArn, ServiceArn: i.ServiceArn}
				}),
				NextToken: next,
			}, nil
		})
}

type ingressVpcConfigurationJSON struct {
	VpcID         string `json:"VpcId"`
	VpcEndpointID string `json:"VpcEndpointId"`
}

func ingressConfigFromWire(in *ingressVpcConfigurationJSON) driver.IngressVpcConfiguration {
	if in == nil {
		return driver.IngressVpcConfiguration{}
	}

	return driver.IngressVpcConfiguration{VpcID: in.VpcID, VpcEndpointID: in.VpcEndpointID}
}

type vpcIngressConnectionJSON struct {
	VpcIngressConnectionName string                       `json:"VpcIngressConnectionName"`
	VpcIngressConnectionArn  string                       `json:"VpcIngressConnectionArn"`
	ServiceArn               string                       `json:"ServiceArn"`
	AccountID                string                       `json:"AccountId"`
	DomainName               string                       `json:"DomainName"`
	Status                   string                       `json:"Status"`
	IngressVpcConfiguration  *ingressVpcConfigurationJSON `json:"IngressVpcConfiguration"`
	CreatedAt                any                          `json:"CreatedAt,omitempty"`
	DeletedAt                any                          `json:"DeletedAt,omitempty"`
}

func ingressToWire(c *driver.VpcIngressConnection) vpcIngressConnectionJSON {
	return vpcIngressConnectionJSON{
		VpcIngressConnectionName: c.VpcIngressConnectionName, VpcIngressConnectionArn: c.VpcIngressConnectionArn,
		ServiceArn: c.ServiceArn, AccountID: c.AccountID, DomainName: c.DomainName, Status: c.Status,
		IngressVpcConfiguration: &ingressVpcConfigurationJSON{
			VpcID: c.IngressVpcConfiguration.VpcID, VpcEndpointID: c.IngressVpcConfiguration.VpcEndpointID,
		},
		CreatedAt: epochSeconds(c.CreatedAt), DeletedAt: epochSeconds(c.DeletedAt),
	}
}

type vpcIngressConnectionResponse struct {
	VpcIngressConnection vpcIngressConnectionJSON `json:"VpcIngressConnection"`
}

type vpcIngressConnectionRef struct {
	VpcIngressConnectionArn string `json:"VpcIngressConnectionArn"`
}

type createVpcIngressConnectionRequest struct {
	VpcIngressConnectionName string                       `json:"VpcIngressConnectionName"`
	ServiceArn               string                       `json:"ServiceArn"`
	IngressVpcConfiguration  *ingressVpcConfigurationJSON `json:"IngressVpcConfiguration"`
	Tags                     []tagJSON                    `json:"Tags"`
}

type updateVpcIngressConnectionRequest struct {
	VpcIngressConnectionArn string                       `json:"VpcIngressConnectionArn"`
	IngressVpcConfiguration *ingressVpcConfigurationJSON `json:"IngressVpcConfiguration"`
}

type listVpcIngressConnectionsRequest struct {
	Filter *struct {
		ServiceArn    string `json:"ServiceArn"`
		VpcEndpointID string `json:"VpcEndpointId"`
	} `json:"Filter"`
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type vpcIngressSummaryJSON struct {
	VpcIngressConnectionArn string `json:"VpcIngressConnectionArn"`
	ServiceArn              string `json:"ServiceArn"`
}

type listVpcIngressConnectionsResponse struct {
	VpcIngressConnectionSummaryList []vpcIngressSummaryJSON `json:"VpcIngressConnectionSummaryList"`
	NextToken                       string                  `json:"NextToken,omitempty"`
}
