package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerCustomDomainRoutes wires the custom domain operations.
func (h *Handler) registerCustomDomainRoutes(d driver.CustomDomains) {
	h.routes["AssociateCustomDomain"] = handle(h, func(ctx context.Context, req *associateCustomDomainRequest) (customDomainResponse, error) {
		res, err := d.AssociateCustomDomain(ctx, req.ServiceArn, req.DomainName, req.EnableWWWSubdomain)
		if err != nil {
			return customDomainResponse{}, err
		}

		return customDomainResultToWire(res), nil
	})
	h.routes["DisassociateCustomDomain"] = handle(h, func(
		ctx context.Context, req *disassociateCustomDomainRequest,
	) (customDomainResponse, error) {
		res, err := d.DisassociateCustomDomain(ctx, req.ServiceArn, req.DomainName)
		if err != nil {
			return customDomainResponse{}, err
		}

		return customDomainResultToWire(res), nil
	})
	h.routes["DescribeCustomDomains"] = handle(h, func(ctx context.Context, req *describeCustomDomainsRequest) (customDomainResponse, error) {
		res, next, err := d.DescribeCustomDomains(ctx, req.ServiceArn, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return customDomainResponse{}, err
		}

		out := customDomainResultToWire(res)
		out.NextToken = next

		return out, nil
	})
}

type associateCustomDomainRequest struct {
	ServiceArn         string `json:"ServiceArn"`
	DomainName         string `json:"DomainName"`
	EnableWWWSubdomain *bool  `json:"EnableWWWSubdomain"`
}

type disassociateCustomDomainRequest struct {
	ServiceArn string `json:"ServiceArn"`
	DomainName string `json:"DomainName"`
}

type describeCustomDomainsRequest struct {
	ServiceArn string `json:"ServiceArn"`
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type certificateValidationRecordJSON struct {
	Name   string `json:"Name"`
	Type   string `json:"Type"`
	Value  string `json:"Value"`
	Status string `json:"Status"`
}

type customDomainJSON struct {
	DomainName                   string                            `json:"DomainName"`
	EnableWWWSubdomain           bool                              `json:"EnableWWWSubdomain"`
	Status                       string                            `json:"Status"`
	CertificateValidationRecords []certificateValidationRecordJSON `json:"CertificateValidationRecords,omitempty"`
}

type vpcDNSTargetJSON struct {
	DomainName              string `json:"DomainName"`
	VpcID                   string `json:"VpcId"`
	VpcIngressConnectionArn string `json:"VpcIngressConnectionArn"`
}

// customDomainResponse is the shared body of the three custom domain operations:
// Associate and Disassociate carry CustomDomain, Describe carries CustomDomains.
type customDomainResponse struct {
	ServiceArn    string              `json:"ServiceArn"`
	DNSTarget     string              `json:"DNSTarget"`
	CustomDomain  *customDomainJSON   `json:"CustomDomain,omitempty"`
	CustomDomains *[]customDomainJSON `json:"CustomDomains,omitempty"`
	VpcDNSTargets []vpcDNSTargetJSON  `json:"VpcDNSTargets"`
	NextToken     string              `json:"NextToken,omitempty"`
}

func customDomainToWire(d *driver.CustomDomain) customDomainJSON {
	out := customDomainJSON{DomainName: d.DomainName, EnableWWWSubdomain: d.EnableWWWSubdomain, Status: d.Status}

	for _, r := range d.CertificateValidationRecords {
		out.CertificateValidationRecords = append(out.CertificateValidationRecords, certificateValidationRecordJSON(r))
	}

	return out
}

func customDomainResultToWire(res *driver.CustomDomainResult) customDomainResponse {
	out := customDomainResponse{
		ServiceArn: res.ServiceArn, DNSTarget: res.DNSTarget, VpcDNSTargets: make([]vpcDNSTargetJSON, len(res.VpcDNSTargets)),
	}

	if res.CustomDomain != nil {
		d := customDomainToWire(res.CustomDomain)
		out.CustomDomain = &d
	}

	// A pointer keeps an empty list on the wire (omitempty would drop it): Describe
	// always carries CustomDomains, Associate and Disassociate never do.
	if res.CustomDomains != nil {
		list := make([]customDomainJSON, len(res.CustomDomains))
		for i := range res.CustomDomains {
			list[i] = customDomainToWire(&res.CustomDomains[i])
		}

		out.CustomDomains = &list
	}

	for i, t := range res.VpcDNSTargets {
		out.VpcDNSTargets[i] = vpcDNSTargetJSON(t)
	}

	return out
}
