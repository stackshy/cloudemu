package apprunner

import (
	"context"
	"net/http"

	ardriver "github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// --- ObservabilityConfiguration ---

func (h *Handler) createObs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createObsRequest) (any, error) {
		cfg, err := h.ar.CreateObservabilityConfiguration(
			ctx, req.ObservabilityConfigurationName, req.TraceConfiguration, tagsToMap(req.Tags),
		)
		if err != nil {
			return nil, err
		}

		return obsResponse{ObservabilityConfiguration: obsToWire(cfg)}, nil
	})
}

func (h *Handler) describeObs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *obsArnRequest) (any, error) {
		cfg, err := h.ar.DescribeObservabilityConfiguration(ctx, req.ObservabilityConfigurationArn)
		if err != nil {
			return nil, err
		}

		return obsResponse{ObservabilityConfiguration: obsToWire(cfg)}, nil
	})
}

func (h *Handler) deleteObs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *obsArnRequest) (any, error) {
		cfg, err := h.ar.DeleteObservabilityConfiguration(ctx, req.ObservabilityConfigurationArn)
		if err != nil {
			return nil, err
		}

		return obsResponse{ObservabilityConfiguration: obsToWire(cfg)}, nil
	})
}

func (h *Handler) listObs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listObsRequest) (any, error) {
		cfgs, token, err := h.ar.ListObservabilityConfigurations(
			ctx, req.ObservabilityConfigurationName, req.LatestOnly, req.NextToken, req.MaxResults,
		)
		if err != nil {
			return nil, err
		}

		items := make([]obsSummaryItem, 0, len(cfgs))

		for i := range cfgs {
			c := &cfgs[i]
			items = append(items, obsSummaryItem{
				ObservabilityConfigurationArn: c.Arn, ObservabilityConfigurationName: c.Name,
				ObservabilityConfigurationRevision: c.Revision,
			})
		}

		return listObsResponse{ObservabilityConfigurationSummaryList: items, NextToken: token}, nil
	})
}

func obsToWire(c *ardriver.ObservabilityConfiguration) wireObs {
	return wireObs{
		ObservabilityConfigurationArn: c.Arn, ObservabilityConfigurationName: c.Name,
		ObservabilityConfigurationRevision: c.Revision, Status: c.Status, Latest: c.Latest,
		TraceConfiguration: c.TraceConfiguration,
		CreatedAt:          epoch(c.CreatedAt), DeletedAt: epoch(c.DeletedAt),
	}
}

// --- VpcConnector ---

func (h *Handler) createVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createVpcConnectorRequest) (any, error) {
		vc, err := h.ar.CreateVpcConnector(ctx, req.VpcConnectorName, req.Subnets, req.SecurityGroups, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(vc)}, nil
	})
}

func (h *Handler) describeVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcConnectorArnRequest) (any, error) {
		vc, err := h.ar.DescribeVpcConnector(ctx, req.VpcConnectorArn)
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(vc)}, nil
	})
}

func (h *Handler) deleteVpcConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcConnectorArnRequest) (any, error) {
		vc, err := h.ar.DeleteVpcConnector(ctx, req.VpcConnectorArn)
		if err != nil {
			return nil, err
		}

		return vpcConnectorResponse{VpcConnector: vpcConnectorToWire(vc)}, nil
	})
}

func (h *Handler) listVpcConnectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listVpcConnectorsRequest) (any, error) {
		vcs, token, err := h.ar.ListVpcConnectors(ctx, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		items := make([]wireVpcConnector, 0, len(vcs))

		for i := range vcs {
			items = append(items, vpcConnectorToWire(&vcs[i]))
		}

		return listVpcConnectorsResponse{VpcConnectors: items, NextToken: token}, nil
	})
}

func vpcConnectorToWire(v *ardriver.VpcConnector) wireVpcConnector {
	return wireVpcConnector{
		VpcConnectorArn: v.Arn, VpcConnectorName: v.Name, VpcConnectorRevision: v.Revision,
		Status: v.Status, Subnets: v.Subnets, SecurityGroups: v.SecurityGroups,
		CreatedAt: epoch(v.CreatedAt), DeletedAt: epoch(v.DeletedAt),
	}
}

// --- VpcIngressConnection ---

func (h *Handler) createVpcIngress(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createVpcIngressRequest) (any, error) {
		vic, err := h.ar.CreateVpcIngressConnection(
			ctx, req.VpcIngressConnectionName, req.ServiceArn, req.IngressVpcConfiguration, tagsToMap(req.Tags),
		)
		if err != nil {
			return nil, err
		}

		return vpcIngressResponse{VpcIngressConnection: vpcIngressToWire(vic)}, nil
	})
}

func (h *Handler) describeVpcIngress(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcIngressArnRequest) (any, error) {
		vic, err := h.ar.DescribeVpcIngressConnection(ctx, req.VpcIngressConnectionArn)
		if err != nil {
			return nil, err
		}

		return vpcIngressResponse{VpcIngressConnection: vpcIngressToWire(vic)}, nil
	})
}

func (h *Handler) deleteVpcIngress(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *vpcIngressArnRequest) (any, error) {
		vic, err := h.ar.DeleteVpcIngressConnection(ctx, req.VpcIngressConnectionArn)
		if err != nil {
			return nil, err
		}

		return vpcIngressResponse{VpcIngressConnection: vpcIngressToWire(vic)}, nil
	})
}

func (h *Handler) updateVpcIngress(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateVpcIngressRequest) (any, error) {
		vic, err := h.ar.UpdateVpcIngressConnection(ctx, req.VpcIngressConnectionArn, req.IngressVpcConfiguration)
		if err != nil {
			return nil, err
		}

		return vpcIngressResponse{VpcIngressConnection: vpcIngressToWire(vic)}, nil
	})
}

func (h *Handler) listVpcIngress(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listVpcIngressRequest) (any, error) {
		var serviceArn, endpointID string
		if req.Filter != nil {
			serviceArn, endpointID = req.Filter.ServiceArn, req.Filter.VpcEndpointID
		}

		vics, token, err := h.ar.ListVpcIngressConnections(ctx, serviceArn, endpointID, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		items := make([]vpcIngressSummaryItem, 0, len(vics))

		for i := range vics {
			items = append(items, vpcIngressSummaryItem{
				VpcIngressConnectionArn: vics[i].Arn, ServiceArn: vics[i].ServiceArn,
			})
		}

		return listVpcIngressResponse{VpcIngressConnectionSummaryList: items, NextToken: token}, nil
	})
}

func vpcIngressToWire(v *ardriver.VpcIngressConnection) wireVpcIngress {
	return wireVpcIngress{
		VpcIngressConnectionArn: v.Arn, VpcIngressConnectionName: v.Name, ServiceArn: v.ServiceArn,
		Status: v.Status, AccountID: v.AccountID, DomainName: v.DomainName,
		IngressVpcConfiguration: v.IngressVpcConfiguration,
		CreatedAt:               epoch(v.CreatedAt), DeletedAt: epoch(v.DeletedAt),
	}
}

// --- CustomDomain ---

func (h *Handler) associateCustomDomain(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *associateCustomDomainRequest) (any, error) {
		enableWWW := req.EnableWWWSubdomain == nil || *req.EnableWWWSubdomain

		cd, dnsTarget, err := h.ar.AssociateCustomDomain(ctx, req.ServiceArn, req.DomainName, enableWWW)
		if err != nil {
			return nil, err
		}

		return associateCustomDomainResponse{
			CustomDomain: customDomainToWire(cd), DNSTarget: dnsTarget,
			ServiceArn: req.ServiceArn, VpcDNSTargets: []any{},
		}, nil
	})
}

func (h *Handler) disassociateCustomDomain(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *disassociateCustomDomainRequest) (any, error) {
		cd, dnsTarget, err := h.ar.DisassociateCustomDomain(ctx, req.ServiceArn, req.DomainName)
		if err != nil {
			return nil, err
		}

		return associateCustomDomainResponse{
			CustomDomain: customDomainToWire(cd), DNSTarget: dnsTarget,
			ServiceArn: req.ServiceArn, VpcDNSTargets: []any{},
		}, nil
	})
}

func (h *Handler) describeCustomDomains(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeCustomDomainsRequest) (any, error) {
		domains, dnsTarget, token, err := h.ar.DescribeCustomDomains(
			ctx, req.ServiceArn, req.NextToken, req.MaxResults,
		)
		if err != nil {
			return nil, err
		}

		items := make([]wireCustomDomain, 0, len(domains))

		for i := range domains {
			items = append(items, customDomainToWire(&domains[i]))
		}

		return describeCustomDomainsResponse{
			CustomDomains: items, DNSTarget: dnsTarget, ServiceArn: req.ServiceArn,
			VpcDNSTargets: []any{}, NextToken: token,
		}, nil
	})
}

func customDomainToWire(c *ardriver.CustomDomain) wireCustomDomain {
	records := make([]wireCertRecord, 0, len(c.CertificateValidationRecords))
	for _, rec := range c.CertificateValidationRecords {
		records = append(records, wireCertRecord{
			Name: rec.Name, Type: rec.Type, Value: rec.Value, Status: rec.Status,
		})
	}

	return wireCustomDomain{
		DomainName: c.DomainName, EnableWWWSubdomain: c.EnableWWWSubdomain,
		Status: c.Status, CertificateValidationRecords: records,
	}
}

// --- Operations ---

func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listOperationsRequest) (any, error) {
		ops, token, err := h.ar.ListOperations(ctx, req.ServiceArn, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		items := make([]wireOperation, 0, len(ops))

		for i := range ops {
			o := &ops[i]
			items = append(items, wireOperation{
				ID: o.ID, Type: o.Type, Status: o.Status, TargetArn: o.TargetArn,
				StartedAt: epoch(o.StartedAt), EndedAt: epoch(o.EndedAt), UpdatedAt: epoch(o.UpdatedAt),
			})
		}

		return listOperationsResponse{OperationSummaryList: items, NextToken: token}, nil
	})
}

// --- Tags ---

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.ar.TagResource(ctx, req.ResourceArn, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.ar.UntagResource(ctx, req.ResourceArn, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsRequest) (any, error) {
		tags, err := h.ar.ListTagsForResource(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return listTagsResponse{Tags: mapToTags(tags)}, nil
	})
}
