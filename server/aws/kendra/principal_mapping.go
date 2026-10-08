package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerPrincipalRoutes wires the principal mapping operations.
func (h *Handler) registerPrincipalRoutes(d driver.PrincipalMappings) {
	h.routes["PutPrincipalMapping"] = handle(h, func(ctx context.Context, req *putPrincipalMappingRequest) (struct{}, error) {
		return ack(d.PutPrincipalMapping(ctx, &driver.PutPrincipalMappingInput{
			IndexID: req.IndexID, DataSourceID: req.DataSourceID, GroupID: req.GroupID,
			GroupMembers: req.GroupMembers, OrderingID: req.OrderingID, RoleArn: req.RoleArn,
		}))
	})
	h.routes["DeletePrincipalMapping"] = handle(h, func(ctx context.Context, req *deletePrincipalMappingRequest) (struct{}, error) {
		return ack(d.DeletePrincipalMapping(ctx, &driver.DeletePrincipalMappingInput{
			IndexID: req.IndexID, DataSourceID: req.DataSourceID, GroupID: req.GroupID, OrderingID: req.OrderingID,
		}))
	})
	h.routes["DescribePrincipalMapping"] = handle(h,
		func(ctx context.Context, req *describePrincipalMappingRequest) (describePrincipalMappingResponse, error) {
			res, err := d.DescribePrincipalMapping(ctx, req.IndexID, req.DataSourceID, req.GroupID)
			if err != nil {
				return describePrincipalMappingResponse{}, err
			}

			out := describePrincipalMappingResponse{
				IndexID: res.IndexID, DataSourceID: res.DataSourceID, GroupID: res.GroupID,
				GroupOrderingIDSummaries: make([]orderingSummaryJSON, len(res.Summaries)),
			}

			for i, s := range res.Summaries {
				out.GroupOrderingIDSummaries[i] = orderingSummaryJSON{
					OrderingID: s.OrderingID, Status: s.Status, ReceivedAt: epochSeconds(s.ReceivedAt),
					LastUpdatedAt: epochSeconds(s.LastUpdatedAt), FailureReason: s.FailureReason,
				}
			}

			return out, nil
		})
	h.routes["ListGroupsOlderThanOrderingId"] = handle(h, func(ctx context.Context, req *listGroupsRequest) (listGroupsResponse, error) {
		groups, next, err := d.ListGroupsOlderThanOrderingID(ctx, &driver.ListGroupsInput{
			IndexID: req.IndexID, DataSourceID: req.DataSourceID, OrderingID: req.OrderingID,
			Page: driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults},
		})
		if err != nil {
			return listGroupsResponse{}, err
		}

		out := listGroupsResponse{GroupsSummaries: make([]groupSummaryJSON, len(groups)), NextToken: next}
		for i, g := range groups {
			out.GroupsSummaries[i] = groupSummaryJSON{GroupID: g.GroupID, OrderingID: g.OrderingID}
		}

		return out, nil
	})
}

type putPrincipalMappingRequest struct {
	IndexID      string  `json:"IndexId"`
	DataSourceID string  `json:"DataSourceId"`
	GroupID      string  `json:"GroupId"`
	GroupMembers rawJSON `json:"GroupMembers"`
	OrderingID   *int64  `json:"OrderingId"`
	RoleArn      string  `json:"RoleArn"`
}

type deletePrincipalMappingRequest struct {
	IndexID      string `json:"IndexId"`
	DataSourceID string `json:"DataSourceId"`
	GroupID      string `json:"GroupId"`
	OrderingID   *int64 `json:"OrderingId"`
}

type describePrincipalMappingRequest struct {
	IndexID      string `json:"IndexId"`
	DataSourceID string `json:"DataSourceId"`
	GroupID      string `json:"GroupId"`
}

type orderingSummaryJSON struct {
	OrderingID    int64  `json:"OrderingId"`
	Status        string `json:"Status"`
	ReceivedAt    int64  `json:"ReceivedAt"`
	LastUpdatedAt int64  `json:"LastUpdatedAt"`
	FailureReason string `json:"FailureReason,omitempty"`
}

type listGroupsRequest struct {
	IndexID      string `json:"IndexId"`
	DataSourceID string `json:"DataSourceId"`
	OrderingID   int64  `json:"OrderingId"`
	MaxResults   int32  `json:"MaxResults"`
	NextToken    string `json:"NextToken"`
}

type describePrincipalMappingResponse struct {
	IndexID                  string                `json:"IndexId"`
	DataSourceID             string                `json:"DataSourceId,omitempty"`
	GroupID                  string                `json:"GroupId"`
	GroupOrderingIDSummaries []orderingSummaryJSON `json:"GroupOrderingIdSummaries"`
}

type groupSummaryJSON struct {
	GroupID    string `json:"GroupId"`
	OrderingID int64  `json:"OrderingId"`
}

type listGroupsResponse struct {
	GroupsSummaries []groupSummaryJSON `json:"GroupsSummaries"`
	NextToken       string             `json:"NextToken,omitempty"`
}
