package apprunner

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerAutoScalingRoutes wires the auto scaling configuration operations.
func (h *Handler) registerAutoScalingRoutes() {
	h.routes["CreateAutoScalingConfiguration"] = h.createAutoScalingConfiguration
	h.routes["DescribeAutoScalingConfiguration"] = h.describeAutoScalingConfiguration
	h.routes["DeleteAutoScalingConfiguration"] = h.deleteAutoScalingConfiguration
	h.routes["ListAutoScalingConfigurations"] = h.listAutoScalingConfigurations
	h.routes["ListAutoScalingConfigurationRevisions"] = h.listAutoScalingConfigurationRevisions
}

// autoScalingConfigurationJSON is the wire shape of a full auto scaling
// configuration resource.
type autoScalingConfigurationJSON struct {
	AutoScalingConfigurationArn      string `json:"AutoScalingConfigurationArn"`
	AutoScalingConfigurationName     string `json:"AutoScalingConfigurationName"`
	AutoScalingConfigurationRevision int32  `json:"AutoScalingConfigurationRevision"`
	Latest                           bool   `json:"Latest"`
	Status                           string `json:"Status"`
	MaxConcurrency                   int32  `json:"MaxConcurrency"`
	MinSize                          int32  `json:"MinSize"`
	MaxSize                          int32  `json:"MaxSize"`
	HasAssociatedService             bool   `json:"HasAssociatedService"`
	IsDefault                        bool   `json:"IsDefault"`
	CreatedAt                        any    `json:"CreatedAt,omitempty"`
	DeletedAt                        any    `json:"DeletedAt,omitempty"`
}

func autoScalingToWire(c *driver.AutoScalingConfiguration) autoScalingConfigurationJSON {
	return autoScalingConfigurationJSON{
		AutoScalingConfigurationArn:      c.AutoScalingConfigurationArn,
		AutoScalingConfigurationName:     c.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: c.AutoScalingConfigurationRevision,
		Latest:                           c.Latest,
		Status:                           c.Status,
		MaxConcurrency:                   c.MaxConcurrency,
		MinSize:                          c.MinSize,
		MaxSize:                          c.MaxSize,
		HasAssociatedService:             c.HasAssociatedService,
		IsDefault:                        c.IsDefault,
		CreatedAt:                        epochSeconds(c.CreatedAt),
		DeletedAt:                        epochSeconds(c.DeletedAt),
	}
}

// autoScalingSummaryListJSON is the wire shape of an AutoScalingConfiguration
// summary in the list operations.
type autoScalingSummaryListJSON struct {
	AutoScalingConfigurationArn      string `json:"AutoScalingConfigurationArn"`
	AutoScalingConfigurationName     string `json:"AutoScalingConfigurationName"`
	AutoScalingConfigurationRevision int32  `json:"AutoScalingConfigurationRevision"`
	Status                           string `json:"Status"`
	HasAssociatedService             bool   `json:"HasAssociatedService"`
	IsDefault                        bool   `json:"IsDefault"`
	CreatedAt                        any    `json:"CreatedAt,omitempty"`
}

func autoScalingSummaryListToWire(c *driver.AutoScalingConfiguration) autoScalingSummaryListJSON {
	return autoScalingSummaryListJSON{
		AutoScalingConfigurationArn:      c.AutoScalingConfigurationArn,
		AutoScalingConfigurationName:     c.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: c.AutoScalingConfigurationRevision,
		Status:                           c.Status,
		HasAssociatedService:             c.HasAssociatedService,
		IsDefault:                        c.IsDefault,
		CreatedAt:                        epochSeconds(c.CreatedAt),
	}
}

type createAutoScalingConfigurationRequest struct {
	AutoScalingConfigurationName string    `json:"AutoScalingConfigurationName"`
	MaxConcurrency               *int32    `json:"MaxConcurrency"`
	MinSize                      *int32    `json:"MinSize"`
	MaxSize                      *int32    `json:"MaxSize"`
	Tags                         []tagJSON `json:"Tags"`
}

type autoScalingConfigurationResponse struct {
	AutoScalingConfiguration autoScalingConfigurationJSON `json:"AutoScalingConfiguration"`
}

func (h *Handler) createAutoScalingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createAutoScalingConfigurationRequest) (any, error) {
		cfg, err := h.apprunner.CreateAutoScalingConfiguration(ctx, &driver.CreateAutoScalingConfigurationInput{
			AutoScalingConfigurationName: req.AutoScalingConfigurationName,
			MaxConcurrency:               req.MaxConcurrency,
			MinSize:                      req.MinSize,
			MaxSize:                      req.MaxSize,
			Tags:                         tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return autoScalingConfigurationResponse{AutoScalingConfiguration: autoScalingToWire(cfg)}, nil
	})
}

type autoScalingArnRequest struct {
	AutoScalingConfigurationArn string `json:"AutoScalingConfigurationArn"`
}

func (h *Handler) describeAutoScalingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *autoScalingArnRequest) (any, error) {
		cfg, err := h.apprunner.DescribeAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn)
		if err != nil {
			return nil, err
		}

		return autoScalingConfigurationResponse{AutoScalingConfiguration: autoScalingToWire(cfg)}, nil
	})
}

func (h *Handler) deleteAutoScalingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *autoScalingArnRequest) (any, error) {
		cfg, err := h.apprunner.DeleteAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn)
		if err != nil {
			return nil, err
		}

		return autoScalingConfigurationResponse{AutoScalingConfiguration: autoScalingToWire(cfg)}, nil
	})
}

type listAutoScalingConfigurationsRequest struct {
	AutoScalingConfigurationName string `json:"AutoScalingConfigurationName"`
	LatestOnly                   bool   `json:"LatestOnly"`
	MaxResults                   int32  `json:"MaxResults"`
	NextToken                    string `json:"NextToken"`
}

type autoScalingSummaryListResponse struct {
	AutoScalingConfigurationSummaryList []autoScalingSummaryListJSON `json:"AutoScalingConfigurationSummaryList"`
	NextToken                           string                       `json:"NextToken,omitempty"`
}

func (h *Handler) listAutoScalingConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listAutoScalingConfigurationsRequest) (any, error) {
		configs, next, err := h.apprunner.ListAutoScalingConfigurations(
			ctx, req.AutoScalingConfigurationName, req.LatestOnly, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return autoScalingSummaryListPage(configs, next), nil
	})
}

type listAutoScalingConfigurationRevisionsRequest struct {
	AutoScalingConfigurationName string `json:"AutoScalingConfigurationName"`
	MaxResults                   int32  `json:"MaxResults"`
	NextToken                    string `json:"NextToken"`
}

func (h *Handler) listAutoScalingConfigurationRevisions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listAutoScalingConfigurationRevisionsRequest) (any, error) {
		configs, next, err := h.apprunner.ListAutoScalingConfigurationRevisions(
			ctx, req.AutoScalingConfigurationName, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return autoScalingSummaryListPage(configs, next), nil
	})
}

// autoScalingSummaryListPage renders a page of configurations as the shared
// summary-list response.
func autoScalingSummaryListPage(
	configs []*driver.AutoScalingConfiguration, next string,
) autoScalingSummaryListResponse {
	return autoScalingSummaryListResponse{
		AutoScalingConfigurationSummaryList: mapWire(configs, autoScalingSummaryListToWire),
		NextToken:                           next,
	}
}
