package apprunner

import (
	"context"
	"net/http"

	ardriver "github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// --- AutoScalingConfiguration ---

func (h *Handler) createASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createASCRequest) (any, error) {
		cfg, err := h.ar.CreateAutoScalingConfiguration(
			ctx, req.AutoScalingConfigurationName, req.MaxConcurrency, req.MaxSize, req.MinSize,
		)
		if err != nil {
			return nil, err
		}

		return ascResponse{AutoScalingConfiguration: ascToWire(cfg)}, nil
	})
}

func (h *Handler) describeASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ascArnRequest) (any, error) {
		cfg, err := h.ar.DescribeAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn)
		if err != nil {
			return nil, err
		}

		return ascResponse{AutoScalingConfiguration: ascToWire(cfg)}, nil
	})
}

func (h *Handler) deleteASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ascArnRequest) (any, error) {
		cfg, err := h.ar.DeleteAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn, req.DeleteAllRevisions)
		if err != nil {
			return nil, err
		}

		return ascResponse{AutoScalingConfiguration: ascToWire(cfg)}, nil
	})
}

func (h *Handler) listASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listASCRequest) (any, error) {
		cfgs, token, err := h.ar.ListAutoScalingConfigurations(
			ctx, req.AutoScalingConfigurationName, req.LatestOnly, req.NextToken, req.MaxResults,
		)
		if err != nil {
			return nil, err
		}

		items := make([]ascSummaryItem, 0, len(cfgs))

		for i := range cfgs {
			c := &cfgs[i]
			items = append(items, ascSummaryItem{
				AutoScalingConfigurationArn: c.Arn, AutoScalingConfigurationName: c.Name,
				AutoScalingConfigurationRevision: c.Revision, Status: c.Status,
				IsDefault: c.IsDefault, HasAssociatedService: c.HasAssociatedService, CreatedAt: epoch(c.CreatedAt),
			})
		}

		return listASCResponse{AutoScalingConfigurationSummaryList: items, NextToken: token}, nil
	})
}

func (h *Handler) updateDefaultASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ascArnRequest) (any, error) {
		cfg, err := h.ar.UpdateDefaultAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn)
		if err != nil {
			return nil, err
		}

		return ascResponse{AutoScalingConfiguration: ascToWire(cfg)}, nil
	})
}

func (h *Handler) listServicesForASC(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listServicesForASCRequest) (any, error) {
		arns, token, err := h.ar.ListServicesForAutoScalingConfiguration(
			ctx, req.AutoScalingConfigurationArn, req.NextToken, req.MaxResults,
		)
		if err != nil {
			return nil, err
		}

		if arns == nil {
			arns = []string{}
		}

		return listServicesForASCResponse{ServiceArnList: arns, NextToken: token}, nil
	})
}

func ascToWire(c *ardriver.AutoScalingConfiguration) wireASC {
	return wireASC{
		AutoScalingConfigurationArn: c.Arn, AutoScalingConfigurationName: c.Name,
		AutoScalingConfigurationRevision: c.Revision, Status: c.Status,
		MaxConcurrency: c.MaxConcurrency, MaxSize: c.MaxSize, MinSize: c.MinSize,
		IsDefault: c.IsDefault, Latest: c.Latest, HasAssociatedService: c.HasAssociatedService,
		CreatedAt: epoch(c.CreatedAt), DeletedAt: epoch(c.DeletedAt),
	}
}

// --- Connection ---

func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createConnectionRequest) (any, error) {
		conn, err := h.ar.CreateConnection(ctx, req.ConnectionName, req.ProviderType, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return connectionResponse{Connection: connectionToWire(conn)}, nil
	})
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *connectionArnRequest) (any, error) {
		conn, err := h.ar.DeleteConnection(ctx, req.ConnectionArn)
		if err != nil {
			return nil, err
		}

		return connectionResponse{Connection: connectionToWire(conn)}, nil
	})
}

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listConnectionsRequest) (any, error) {
		conns, token, err := h.ar.ListConnections(ctx, req.ConnectionName, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		items := make([]connectionSummaryItem, 0, len(conns))

		for i := range conns {
			c := &conns[i]
			items = append(items, connectionSummaryItem{
				ConnectionArn: c.Arn, ConnectionName: c.Name, ProviderType: c.ProviderType,
				Status: c.Status, CreatedAt: epoch(c.CreatedAt),
			})
		}

		return listConnectionsResponse{ConnectionSummaryList: items, NextToken: token}, nil
	})
}

func connectionToWire(c *ardriver.Connection) wireConnection {
	return wireConnection{
		ConnectionArn: c.Arn, ConnectionName: c.Name, ProviderType: c.ProviderType,
		Status: c.Status, CreatedAt: epoch(c.CreatedAt),
	}
}
