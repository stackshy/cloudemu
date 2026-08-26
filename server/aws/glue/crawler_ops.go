package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type crawlerJSON struct {
	Name                         string         `json:"Name"`
	Role                         string         `json:"Role,omitempty"`
	DatabaseName                 string         `json:"DatabaseName,omitempty"`
	Description                  string         `json:"Description,omitempty"`
	Targets                      map[string]any `json:"Targets,omitempty"`
	Classifiers                  []string       `json:"Classifiers,omitempty"`
	TablePrefix                  string         `json:"TablePrefix,omitempty"`
	State                        string         `json:"State,omitempty"`
	Schedule                     map[string]any `json:"Schedule,omitempty"`
	Configuration                string         `json:"Configuration,omitempty"`
	SchemaChangePolicy           map[string]any `json:"SchemaChangePolicy,omitempty"`
	RecrawlPolicy                map[string]any `json:"RecrawlPolicy,omitempty"`
	LineageConfiguration         map[string]any `json:"LineageConfiguration,omitempty"`
	CrawlerSecurityConfiguration string         `json:"CrawlerSecurityConfiguration,omitempty"`
	CreationTime                 *float64       `json:"CreationTime,omitempty"`
	LastUpdated                  *float64       `json:"LastUpdated,omitempty"`
	Version                      int64          `json:"Version,omitempty"`
}

func crawlerToWire(c *driver.Crawler) crawlerJSON {
	var sched map[string]any
	if c.Schedule != "" {
		sched = map[string]any{"ScheduleExpression": c.Schedule, "State": "SCHEDULED"}
	}

	return crawlerJSON{
		Name: c.Name, Role: c.Role, DatabaseName: c.DatabaseName, Description: c.Description,
		Targets: c.Targets, Classifiers: c.Classifiers, TablePrefix: c.TablePrefix, State: c.State,
		Schedule: sched, Configuration: c.Configuration, SchemaChangePolicy: c.SchemaChangePolicy,
		RecrawlPolicy: c.RecrawlPolicy, LineageConfiguration: c.LineageConfiguration,
		CrawlerSecurityConfiguration: c.SecurityConfiguration, CreationTime: epochOrNil(c.CreationTime),
		LastUpdated: epochOrNil(c.LastUpdated), Version: c.Version,
	}
}

type createCrawlerRequest struct {
	Name                         string            `json:"Name"`
	Role                         string            `json:"Role"`
	DatabaseName                 string            `json:"DatabaseName"`
	Description                  string            `json:"Description"`
	Targets                      map[string]any    `json:"Targets"`
	Classifiers                  []string          `json:"Classifiers"`
	TablePrefix                  string            `json:"TablePrefix"`
	Schedule                     string            `json:"Schedule"`
	Configuration                string            `json:"Configuration"`
	SchemaChangePolicy           map[string]any    `json:"SchemaChangePolicy"`
	RecrawlPolicy                map[string]any    `json:"RecrawlPolicy"`
	LineageConfiguration         map[string]any    `json:"LineageConfiguration"`
	CrawlerSecurityConfiguration string            `json:"CrawlerSecurityConfiguration"`
	Tags                         map[string]string `json:"Tags"`
}

//nolint:gocritic // hugeParam: request is decoded once; a pointer adds no value here
func crawlerFromRequest(req createCrawlerRequest) driver.Crawler {
	return driver.Crawler{
		Name: req.Name, Role: req.Role, DatabaseName: req.DatabaseName, Description: req.Description,
		Targets: req.Targets, Classifiers: req.Classifiers, TablePrefix: req.TablePrefix,
		Schedule: req.Schedule, Configuration: req.Configuration, SchemaChangePolicy: req.SchemaChangePolicy,
		RecrawlPolicy: req.RecrawlPolicy, LineageConfiguration: req.LineageConfiguration,
		SecurityConfiguration: req.CrawlerSecurityConfiguration, Tags: req.Tags,
	}
}

func (h *Handler) createCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createCrawlerRequest) (any, error) {
		if err := h.glue.CreateCrawler(ctx, crawlerFromRequest(*req)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type crawlerNameRequest struct {
	Name string `json:"Name"`
}

type getCrawlerResponse struct {
	Crawler crawlerJSON `json:"Crawler"`
}

func (h *Handler) getCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *crawlerNameRequest) (any, error) {
		c, err := h.glue.GetCrawler(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getCrawlerResponse{Crawler: crawlerToWire(c)}, nil
	})
}

func (h *Handler) updateCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createCrawlerRequest) (any, error) {
		if err := h.glue.UpdateCrawler(ctx, req.Name, crawlerFromRequest(*req)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *crawlerNameRequest) (any, error) {
		if err := h.glue.DeleteCrawler(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type paginationRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

func pageOf(req paginationRequest) driver.TablePagination {
	return driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults}
}

type getCrawlersResponse struct {
	Crawlers  []crawlerJSON `json:"Crawlers"`
	NextToken string        `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getCrawlers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		cs, next, err := h.glue.GetCrawlers(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]crawlerJSON, 0, len(cs))
		for i := range cs {
			out = append(out, crawlerToWire(&cs[i]))
		}

		return getCrawlersResponse{Crawlers: out, NextToken: next}, nil
	})
}

type listNamesResponse struct {
	CrawlerNames []string `json:"CrawlerNames"`
	NextToken    string   `json:"NextToken,omitempty"`
}

func (h *Handler) listCrawlers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListCrawlers(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listNamesResponse{CrawlerNames: names, NextToken: next}, nil
	})
}

func (h *Handler) startCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *crawlerNameRequest) (any, error) {
		if err := h.glue.StartCrawler(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopCrawler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *crawlerNameRequest) (any, error) {
		if err := h.glue.StopCrawler(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type batchGetCrawlersRequest struct {
	CrawlerNames []string `json:"CrawlerNames"`
}

type batchGetCrawlersResponse struct {
	Crawlers         []crawlerJSON `json:"Crawlers"`
	CrawlersNotFound []string      `json:"CrawlersNotFound,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetCrawlers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetCrawlersRequest) (any, error) {
		found, notFound, err := h.glue.BatchGetCrawlers(ctx, req.CrawlerNames)
		if err != nil {
			return nil, err
		}

		out := make([]crawlerJSON, 0, len(found))
		for i := range found {
			out = append(out, crawlerToWire(&found[i]))
		}

		return batchGetCrawlersResponse{Crawlers: out, CrawlersNotFound: notFound}, nil
	})
}
