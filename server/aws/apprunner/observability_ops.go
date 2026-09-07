package apprunner

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerObservabilityRoutes wires the observability configuration operations.
func (h *Handler) registerObservabilityRoutes() {
	h.routes["CreateObservabilityConfiguration"] = h.createObservabilityConfiguration
	h.routes["DescribeObservabilityConfiguration"] = h.describeObservabilityConfiguration
	h.routes["DeleteObservabilityConfiguration"] = h.deleteObservabilityConfiguration
	h.routes["ListObservabilityConfigurations"] = h.listObservabilityConfigurations
	h.routes["ListObservabilityConfigurationRevisions"] = h.listObservabilityConfigurationRevisions
}

type traceConfigurationJSON struct {
	Vendor string `json:"Vendor,omitempty"`
}

// observabilityConfigurationJSON is the wire shape of a full observability
// configuration resource.
type observabilityConfigurationJSON struct {
	ObservabilityConfigurationArn      string                  `json:"ObservabilityConfigurationArn"`
	ObservabilityConfigurationName     string                  `json:"ObservabilityConfigurationName"`
	ObservabilityConfigurationRevision int32                   `json:"ObservabilityConfigurationRevision"`
	Latest                             bool                    `json:"Latest"`
	Status                             string                  `json:"Status"`
	TraceConfiguration                 *traceConfigurationJSON `json:"TraceConfiguration,omitempty"`
	CreatedAt                          any                     `json:"CreatedAt,omitempty"`
	DeletedAt                          any                     `json:"DeletedAt,omitempty"`
}

func observabilityToWire(c *driver.ObservabilityConfiguration) observabilityConfigurationJSON {
	out := observabilityConfigurationJSON{
		ObservabilityConfigurationArn:      c.ObservabilityConfigurationArn,
		ObservabilityConfigurationName:     c.ObservabilityConfigurationName,
		ObservabilityConfigurationRevision: c.ObservabilityConfigurationRevision,
		Latest:                             c.Latest,
		Status:                             c.Status,
		CreatedAt:                          epochSeconds(c.CreatedAt),
		DeletedAt:                          epochSeconds(c.DeletedAt),
	}
	if c.TraceConfiguration != nil {
		out.TraceConfiguration = &traceConfigurationJSON{Vendor: c.TraceConfiguration.Vendor}
	}

	return out
}

// observabilitySummaryJSON is the wire shape of an observability configuration
// summary in the list operations.
type observabilitySummaryJSON struct {
	ObservabilityConfigurationArn      string `json:"ObservabilityConfigurationArn"`
	ObservabilityConfigurationName     string `json:"ObservabilityConfigurationName"`
	ObservabilityConfigurationRevision int32  `json:"ObservabilityConfigurationRevision"`
}

func observabilitySummaryToWire(c *driver.ObservabilityConfiguration) observabilitySummaryJSON {
	return observabilitySummaryJSON{
		ObservabilityConfigurationArn:      c.ObservabilityConfigurationArn,
		ObservabilityConfigurationName:     c.ObservabilityConfigurationName,
		ObservabilityConfigurationRevision: c.ObservabilityConfigurationRevision,
	}
}

type createObservabilityConfigurationRequest struct {
	ObservabilityConfigurationName string                  `json:"ObservabilityConfigurationName"`
	TraceConfiguration             *traceConfigurationJSON `json:"TraceConfiguration"`
	Tags                           []tagJSON               `json:"Tags"`
}

type observabilityConfigurationResponse struct {
	ObservabilityConfiguration observabilityConfigurationJSON `json:"ObservabilityConfiguration"`
}

func (h *Handler) createObservabilityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createObservabilityConfigurationRequest) (any, error) {
		in := &driver.CreateObservabilityConfigurationInput{
			ObservabilityConfigurationName: req.ObservabilityConfigurationName,
			Tags:                           tagsFromWire(req.Tags),
		}
		if req.TraceConfiguration != nil {
			in.TraceConfiguration = &driver.TraceConfiguration{Vendor: req.TraceConfiguration.Vendor}
		}

		cfg, err := h.apprunner.CreateObservabilityConfiguration(ctx, in)
		if err != nil {
			return nil, err
		}

		return observabilityConfigurationResponse{ObservabilityConfiguration: observabilityToWire(cfg)}, nil
	})
}

type observabilityArnRequest struct {
	ObservabilityConfigurationArn string `json:"ObservabilityConfigurationArn"`
}

func (h *Handler) describeObservabilityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *observabilityArnRequest) (any, error) {
		cfg, err := h.apprunner.DescribeObservabilityConfiguration(ctx, req.ObservabilityConfigurationArn)
		if err != nil {
			return nil, err
		}

		return observabilityConfigurationResponse{ObservabilityConfiguration: observabilityToWire(cfg)}, nil
	})
}

func (h *Handler) deleteObservabilityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *observabilityArnRequest) (any, error) {
		cfg, err := h.apprunner.DeleteObservabilityConfiguration(ctx, req.ObservabilityConfigurationArn)
		if err != nil {
			return nil, err
		}

		return observabilityConfigurationResponse{ObservabilityConfiguration: observabilityToWire(cfg)}, nil
	})
}

type listObservabilityConfigurationsRequest struct {
	ObservabilityConfigurationName string `json:"ObservabilityConfigurationName"`
	LatestOnly                     bool   `json:"LatestOnly"`
	MaxResults                     int32  `json:"MaxResults"`
	NextToken                      string `json:"NextToken"`
}

type observabilitySummaryListResponse struct {
	ObservabilityConfigurationSummaryList []observabilitySummaryJSON `json:"ObservabilityConfigurationSummaryList"`
	NextToken                             string                     `json:"NextToken,omitempty"`
}

func (h *Handler) listObservabilityConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listObservabilityConfigurationsRequest) (any, error) {
		configs, next, err := h.apprunner.ListObservabilityConfigurations(
			ctx, req.ObservabilityConfigurationName, req.LatestOnly, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return observabilitySummaryListPage(configs, next), nil
	})
}

type listObservabilityConfigurationRevisionsRequest struct {
	ObservabilityConfigurationName string `json:"ObservabilityConfigurationName"`
	MaxResults                     int32  `json:"MaxResults"`
	NextToken                      string `json:"NextToken"`
}

func (h *Handler) listObservabilityConfigurationRevisions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listObservabilityConfigurationRevisionsRequest) (any, error) {
		configs, next, err := h.apprunner.ListObservabilityConfigurationRevisions(
			ctx, req.ObservabilityConfigurationName, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return observabilitySummaryListPage(configs, next), nil
	})
}

// observabilitySummaryListPage renders a page of configurations as the shared
// summary-list response.
func observabilitySummaryListPage(
	configs []*driver.ObservabilityConfiguration, next string,
) observabilitySummaryListResponse {
	return observabilitySummaryListResponse{
		ObservabilityConfigurationSummaryList: mapWire(configs, observabilitySummaryToWire),
		NextToken:                             next,
	}
}
