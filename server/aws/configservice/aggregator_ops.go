package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

type putAggregatorReq struct {
	ConfigurationAggregatorName   string              `json:"ConfigurationAggregatorName"`
	AccountAggregationSources     []accountSourceJSON `json:"AccountAggregationSources"`
	OrganizationAggregationSource *orgSourceJSON      `json:"OrganizationAggregationSource"`
	Tags                          []tag               `json:"Tags"`
}

type putAggregatorResp struct {
	ConfigurationAggregator aggregatorJSON `json:"ConfigurationAggregator"`
}

func (h *Handler) putConfigurationAggregator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putAggregatorReq) (any, error) {
		agg := cfgdriver.ConfigurationAggregator{
			Name:           req.ConfigurationAggregatorName,
			AccountSources: accountSourcesToDriver(req.AccountAggregationSources),
			Tags:           tagsToMap(req.Tags),
		}

		if req.OrganizationAggregationSource != nil {
			agg.OrganizationSource = &cfgdriver.OrganizationAggregationSource{
				RoleARN:       req.OrganizationAggregationSource.RoleArn,
				AllAwsRegions: req.OrganizationAggregationSource.AllAwsRegions,
				AwsRegions:    req.OrganizationAggregationSource.AwsRegions,
			}
		}

		out, err := h.cfg.PutConfigurationAggregator(ctx, agg)
		if err != nil {
			return nil, err
		}

		return putAggregatorResp{ConfigurationAggregator: aggToWire(&out)}, nil
	})
}

type aggregatorNamesReq struct {
	ConfigurationAggregatorNames []string `json:"ConfigurationAggregatorNames"`
	NextToken                    string   `json:"NextToken"`
	Limit                        int32    `json:"Limit"`
}

type describeAggregatorsResp struct {
	ConfigurationAggregators []aggregatorJSON `json:"ConfigurationAggregators"`
	NextToken                string           `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConfigurationAggregators(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggregatorNamesReq) (any, error) {
		aggs, next, err := h.cfg.DescribeConfigurationAggregators(
			ctx, req.ConfigurationAggregatorNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]aggregatorJSON, 0, len(aggs))
		for i := range aggs {
			out = append(out, aggToWire(&aggs[i]))
		}

		return describeAggregatorsResp{ConfigurationAggregators: out, NextToken: next}, nil
	})
}

type aggregatorNameReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	NextToken                   string `json:"NextToken"`
	Limit                       int32  `json:"Limit"`
}

func (h *Handler) deleteConfigurationAggregator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggregatorNameReq) (any, error) {
		if err := h.cfg.DeleteConfigurationAggregator(ctx, req.ConfigurationAggregatorName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type aggregatorSourcesStatusResp struct {
	AggregatedSourceStatusList []struct {
		SourceID         string `json:"SourceId,omitempty"`
		LastUpdateStatus string `json:"LastUpdateStatus,omitempty"`
	} `json:"AggregatedSourceStatusList"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) describeConfigurationAggregatorSourcesStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggregatorNameReq) (any, error) {
		aggs, next, err := h.cfg.DescribeConfigurationAggregatorSourcesStatus(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		var resp aggregatorSourcesStatusResp

		resp.NextToken = next
		for range aggs {
			resp.AggregatedSourceStatusList = append(resp.AggregatedSourceStatusList, struct {
				SourceID         string `json:"SourceId,omitempty"`
				LastUpdateStatus string `json:"LastUpdateStatus,omitempty"`
			}{SourceID: req.ConfigurationAggregatorName, LastUpdateStatus: "SUCCEEDED"})
		}

		return resp, nil
	})
}

type putAuthReq struct {
	AuthorizedAccountID string `json:"AuthorizedAccountId"`
	AuthorizedAwsRegion string `json:"AuthorizedAwsRegion"`
	Tags                []tag  `json:"Tags"`
}

type putAuthResp struct {
	AggregationAuthorization aggregationAuthJSON `json:"AggregationAuthorization"`
}

func (h *Handler) putAggregationAuthorization(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putAuthReq) (any, error) {
		auth, err := h.cfg.PutAggregationAuthorization(
			ctx, req.AuthorizedAccountID, req.AuthorizedAwsRegion, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return putAuthResp{AggregationAuthorization: authToWire(&auth)}, nil
	})
}

type describeAuthsResp struct {
	AggregationAuthorizations []aggregationAuthJSON `json:"AggregationAuthorizations"`
	NextToken                 string                `json:"NextToken,omitempty"`
}

func (h *Handler) describeAggregationAuthorizations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		auths, next, err := h.cfg.DescribeAggregationAuthorizations(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]aggregationAuthJSON, 0, len(auths))
		for i := range auths {
			out = append(out, authToWire(&auths[i]))
		}

		return describeAuthsResp{AggregationAuthorizations: out, NextToken: next}, nil
	})
}

type deleteAuthReq struct {
	AuthorizedAccountID string `json:"AuthorizedAccountId"`
	AuthorizedAwsRegion string `json:"AuthorizedAwsRegion"`
}

func (h *Handler) deleteAggregationAuthorization(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteAuthReq) (any, error) {
		if err := h.cfg.DeleteAggregationAuthorization(
			ctx, req.AuthorizedAccountID, req.AuthorizedAwsRegion); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type pendingRequestJSON struct {
	RequesterAccountID string `json:"RequesterAccountId,omitempty"`
	RequesterAwsRegion string `json:"RequesterAwsRegion,omitempty"`
}

type describePendingResp struct {
	PendingAggregationRequests []pendingRequestJSON `json:"PendingAggregationRequests"`
	NextToken                  string               `json:"NextToken,omitempty"`
}

func (h *Handler) describePendingAggregationRequests(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		reqs, next, err := h.cfg.DescribePendingAggregationRequests(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]pendingRequestJSON, 0, len(reqs))
		for i := range reqs {
			out = append(out, pendingRequestJSON{
				RequesterAccountID: reqs[i].RequesterAccountID, RequesterAwsRegion: reqs[i].RequesterAwsRegion,
			})
		}

		return describePendingResp{PendingAggregationRequests: out, NextToken: next}, nil
	})
}

type deletePendingReq struct {
	RequesterAccountID string `json:"RequesterAccountId"`
	RequesterAwsRegion string `json:"RequesterAwsRegion"`
}

func (h *Handler) deletePendingAggregationRequest(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deletePendingReq) (any, error) {
		if err := h.cfg.DeletePendingAggregationRequest(
			ctx, req.RequesterAccountID, req.RequesterAwsRegion); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
