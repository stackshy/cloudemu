package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerAccessControlRoutes wires the access control configuration operations.
func (h *Handler) registerAccessControlRoutes(d driver.AccessControls) {
	h.routes["CreateAccessControlConfiguration"] = handle(h,
		func(ctx context.Context, req *createAccessControlRequest) (idResponse, error) {
			a, err := d.CreateAccessControlConfiguration(ctx, &driver.CreateAccessControlInput{
				IndexID: req.IndexID, Name: req.Name, Description: req.Description,
				AccessControlList: req.AccessControlList, HierarchicalAccessControlList: req.HierarchicalAccessControlList,
				ClientToken: req.ClientToken,
			})
			if err != nil {
				return idResponse{}, err
			}

			return idResponse{ID: a.ID}, nil
		})
	h.routes["DescribeAccessControlConfiguration"] = handle(h,
		func(ctx context.Context, req *childRef) (describeAccessControlResponse, error) {
			a, err := d.DescribeAccessControlConfiguration(ctx, req.IndexID, req.ID)
			if err != nil {
				return describeAccessControlResponse{}, err
			}

			return describeAccessControlResponse{
				Name: a.Name, Description: a.Description, AccessControlList: a.AccessControlList,
				HierarchicalAccessControlList: a.HierarchicalAccessControlList, ErrorMessage: a.ErrorMessage,
			}, nil
		})
	h.routes["UpdateAccessControlConfiguration"] = handle(h,
		func(ctx context.Context, req *updateAccessControlRequest) (struct{}, error) {
			return ack(d.UpdateAccessControlConfiguration(ctx, &driver.UpdateAccessControlInput{
				IndexID: req.IndexID, ID: req.ID, Name: req.Name, Description: req.Description,
				AccessControlList: req.AccessControlList, HierarchicalAccessControlList: req.HierarchicalAccessControlList,
			}))
		})
	h.routes["ListAccessControlConfigurations"] = handle(h,
		func(ctx context.Context, req *listChildrenRequest) (listAccessControlsResponse, error) {
			items, next, err := d.ListAccessControlConfigurations(ctx, req.IndexID, req.page())
			if err != nil {
				return listAccessControlsResponse{}, err
			}

			out := listAccessControlsResponse{AccessControlConfigurations: make([]idResponse, len(items)), NextToken: next}
			for i := range items {
				out.AccessControlConfigurations[i] = idResponse{ID: items[i].ID}
			}

			return out, nil
		})
	h.routes["DeleteAccessControlConfiguration"] = handle(h, func(ctx context.Context, req *childRef) (struct{}, error) {
		return ack(d.DeleteAccessControlConfiguration(ctx, req.IndexID, req.ID))
	})
}

type createAccessControlRequest struct {
	IndexID                       string  `json:"IndexId"`
	Name                          string  `json:"Name"`
	Description                   string  `json:"Description"`
	AccessControlList             rawJSON `json:"AccessControlList"`
	HierarchicalAccessControlList rawJSON `json:"HierarchicalAccessControlList"`
	ClientToken                   string  `json:"ClientToken"`
}

type updateAccessControlRequest struct {
	ID                            string  `json:"Id"`
	IndexID                       string  `json:"IndexId"`
	Name                          *string `json:"Name"`
	Description                   *string `json:"Description"`
	AccessControlList             rawJSON `json:"AccessControlList"`
	HierarchicalAccessControlList rawJSON `json:"HierarchicalAccessControlList"`
}

type describeAccessControlResponse struct {
	Name                          string  `json:"Name"`
	Description                   string  `json:"Description,omitempty"`
	AccessControlList             rawJSON `json:"AccessControlList,omitempty"`
	HierarchicalAccessControlList rawJSON `json:"HierarchicalAccessControlList,omitempty"`
	ErrorMessage                  string  `json:"ErrorMessage,omitempty"`
}

type listAccessControlsResponse struct {
	AccessControlConfigurations []idResponse `json:"AccessControlConfigurations"`
	NextToken                   string       `json:"NextToken,omitempty"`
}
