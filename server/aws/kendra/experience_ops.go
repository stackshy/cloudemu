package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerExperienceRoutes wires the search experience operations.
//
//nolint:dupl // the list handlers of the index children share one shape
func (h *Handler) registerExperienceRoutes(d driver.Experiences) {
	h.routes["CreateExperience"] = handle(h, func(ctx context.Context, req *createExperienceRequest) (idResponse, error) {
		e, err := d.CreateExperience(ctx, &driver.CreateExperienceInput{
			IndexID: req.IndexID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			Configuration: req.Configuration, ClientToken: req.ClientToken,
		})
		if err != nil {
			return idResponse{}, err
		}

		return idResponse{ID: e.ID}, nil
	})
	h.routes["DescribeExperience"] = handle(h, func(ctx context.Context, req *childRef) (describeExperienceResponse, error) {
		e, err := d.DescribeExperience(ctx, req.IndexID, req.ID)
		if err != nil {
			return describeExperienceResponse{}, err
		}

		return describeExperienceResponse{
			ID: e.ID, IndexID: e.IndexID, Name: e.Name, Description: e.Description, RoleArn: e.RoleArn,
			Configuration: e.Configuration, Status: e.Status, ErrorMessage: e.ErrorMessage,
			Endpoints: endpointsToWire(e.Endpoints), CreatedAt: epochSeconds(e.CreatedAt), UpdatedAt: epochSeconds(e.UpdatedAt),
		}, nil
	})
	h.routes["UpdateExperience"] = handle(h, func(ctx context.Context, req *updateExperienceRequest) (struct{}, error) {
		return ack(d.UpdateExperience(ctx, &driver.UpdateExperienceInput{
			IndexID: req.IndexID, ID: req.ID, Name: req.Name, Description: req.Description, RoleArn: req.RoleArn,
			Configuration: req.Configuration,
		}))
	})
	h.routes["ListExperiences"] = handle(h, func(ctx context.Context, req *listChildrenRequest) (listExperiencesResponse, error) {
		items, next, err := d.ListExperiences(ctx, req.IndexID, req.page())
		if err != nil {
			return listExperiencesResponse{}, err
		}

		out := listExperiencesResponse{SummaryItems: make([]experienceSummaryJSON, len(items)), NextToken: next}

		for i := range items {
			e := &items[i]
			out.SummaryItems[i] = experienceSummaryJSON{
				ID: e.ID, Name: e.Name, Status: e.Status, Endpoints: endpointsToWire(e.Endpoints), CreatedAt: epochSeconds(e.CreatedAt),
			}
		}

		return out, nil
	})
	h.routes["DeleteExperience"] = handle(h, func(ctx context.Context, req *childRef) (struct{}, error) {
		return ack(d.DeleteExperience(ctx, req.IndexID, req.ID))
	})
}

type createExperienceRequest struct {
	IndexID       string  `json:"IndexId"`
	Name          string  `json:"Name"`
	Description   string  `json:"Description"`
	RoleArn       string  `json:"RoleArn"`
	Configuration rawJSON `json:"Configuration"`
	ClientToken   string  `json:"ClientToken"`
}

type updateExperienceRequest struct {
	ID            string  `json:"Id"`
	IndexID       string  `json:"IndexId"`
	Name          *string `json:"Name"`
	Description   *string `json:"Description"`
	RoleArn       *string `json:"RoleArn"`
	Configuration rawJSON `json:"Configuration"`
}

type experienceEndpointJSON struct {
	Endpoint     string `json:"Endpoint"`
	EndpointType string `json:"EndpointType"`
}

func endpointsToWire(in []driver.ExperienceEndpoint) []experienceEndpointJSON {
	out := make([]experienceEndpointJSON, len(in))
	for i, ep := range in {
		out[i] = experienceEndpointJSON(ep)
	}

	return out
}

type describeExperienceResponse struct {
	ID            string                   `json:"Id"`
	IndexID       string                   `json:"IndexId"`
	Name          string                   `json:"Name"`
	Description   string                   `json:"Description,omitempty"`
	RoleArn       string                   `json:"RoleArn,omitempty"`
	Configuration rawJSON                  `json:"Configuration,omitempty"`
	Status        string                   `json:"Status"`
	ErrorMessage  string                   `json:"ErrorMessage,omitempty"`
	Endpoints     []experienceEndpointJSON `json:"Endpoints"`
	CreatedAt     int64                    `json:"CreatedAt"`
	UpdatedAt     int64                    `json:"UpdatedAt"`
}

type experienceSummaryJSON struct {
	ID        string                   `json:"Id"`
	Name      string                   `json:"Name"`
	Status    string                   `json:"Status"`
	Endpoints []experienceEndpointJSON `json:"Endpoints"`
	CreatedAt int64                    `json:"CreatedAt"`
}

type listExperiencesResponse struct {
	SummaryItems []experienceSummaryJSON `json:"SummaryItems"`
	NextToken    string                  `json:"NextToken,omitempty"`
}
