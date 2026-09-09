package healthlake

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// registerDatastoreRoutes wires the FHIR data-store operations.
func (h *Handler) registerDatastoreRoutes() {
	h.routes["CreateFHIRDatastore"] = h.createFHIRDatastore
	h.routes["DescribeFHIRDatastore"] = h.describeFHIRDatastore
	h.routes["DeleteFHIRDatastore"] = h.deleteFHIRDatastore
	h.routes["ListFHIRDatastores"] = h.listFHIRDatastores
}

type createFHIRDatastoreRequest struct {
	DatastoreName                 string                      `json:"DatastoreName"`
	DatastoreTypeVersion          string                      `json:"DatastoreTypeVersion"`
	SseConfiguration              *sseConfigurationJSON       `json:"SseConfiguration"`
	PreloadDataConfig             *preloadDataConfigJSON      `json:"PreloadDataConfig"`
	IdentityProviderConfiguration *identityProviderConfigJSON `json:"IdentityProviderConfiguration"`
	Tags                          []tagJSON                   `json:"Tags"`
}

// createFHIRDatastoreResponse is the wire shape of CreateFHIRDatastore's
// result: the identity fields, not the full properties block.
type createFHIRDatastoreResponse struct {
	DatastoreID       string `json:"DatastoreId"`
	DatastoreArn      string `json:"DatastoreArn"`
	DatastoreStatus   string `json:"DatastoreStatus"`
	DatastoreEndpoint string `json:"DatastoreEndpoint"`
}

func (h *Handler) createFHIRDatastore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createFHIRDatastoreRequest) (any, error) {
		ds, err := h.healthlake.CreateFHIRDatastore(ctx, &driver.CreateFHIRDatastoreInput{
			DatastoreName:                 req.DatastoreName,
			DatastoreTypeVersion:          req.DatastoreTypeVersion,
			SseConfiguration:              sseFromWire(req.SseConfiguration),
			PreloadDataConfig:             preloadFromWire(req.PreloadDataConfig),
			IdentityProviderConfiguration: identityProviderFromWire(req.IdentityProviderConfiguration),
			Tags:                          tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createFHIRDatastoreResponse{
			DatastoreID:       ds.DatastoreID,
			DatastoreArn:      ds.DatastoreArn,
			DatastoreStatus:   ds.DatastoreStatus,
			DatastoreEndpoint: ds.DatastoreEndpoint,
		}, nil
	})
}

type describeFHIRDatastoreRequest struct {
	DatastoreID string `json:"DatastoreId"`
}

type describeFHIRDatastoreResponse struct {
	DatastoreProperties datastorePropertiesJSON `json:"DatastoreProperties"`
}

func (h *Handler) describeFHIRDatastore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeFHIRDatastoreRequest) (any, error) {
		ds, err := h.healthlake.DescribeFHIRDatastore(ctx, req.DatastoreID)
		if err != nil {
			return nil, err
		}

		return describeFHIRDatastoreResponse{DatastoreProperties: toDatastoreProperties(ds)}, nil
	})
}

type deleteFHIRDatastoreRequest struct {
	DatastoreID string `json:"DatastoreId"`
}

// deleteFHIRDatastoreResponse is the wire shape of DeleteFHIRDatastore's
// result: the identity fields and the (DELETED) status.
type deleteFHIRDatastoreResponse struct {
	DatastoreID       string `json:"DatastoreId"`
	DatastoreArn      string `json:"DatastoreArn"`
	DatastoreStatus   string `json:"DatastoreStatus"`
	DatastoreEndpoint string `json:"DatastoreEndpoint"`
}

func (h *Handler) deleteFHIRDatastore(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteFHIRDatastoreRequest) (any, error) {
		ds, err := h.healthlake.DeleteFHIRDatastore(ctx, req.DatastoreID)
		if err != nil {
			return nil, err
		}

		return deleteFHIRDatastoreResponse{
			DatastoreID:       ds.DatastoreID,
			DatastoreArn:      ds.DatastoreArn,
			DatastoreStatus:   ds.DatastoreStatus,
			DatastoreEndpoint: ds.DatastoreEndpoint,
		}, nil
	})
}

type datastoreFilterJSON struct {
	DatastoreName   string `json:"DatastoreName"`
	DatastoreStatus string `json:"DatastoreStatus"`
}

type listFHIRDatastoresRequest struct {
	Filter     *datastoreFilterJSON `json:"Filter"`
	MaxResults int32                `json:"MaxResults"`
	NextToken  string               `json:"NextToken"`
}

type listFHIRDatastoresResponse struct {
	DatastorePropertiesList []datastorePropertiesJSON `json:"DatastorePropertiesList"`
	NextToken               string                    `json:"NextToken,omitempty"`
}

func (h *Handler) listFHIRDatastores(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listFHIRDatastoresRequest) (any, error) {
		var filter driver.ListFilter
		if req.Filter != nil {
			filter = driver.ListFilter{
				DatastoreName:   req.Filter.DatastoreName,
				DatastoreStatus: req.Filter.DatastoreStatus,
			}
		}

		datastores, next, err := h.healthlake.ListFHIRDatastores(ctx, filter, driver.Page{
			NextToken: req.NextToken, MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}

		resp := listFHIRDatastoresResponse{
			DatastorePropertiesList: make([]datastorePropertiesJSON, 0, len(datastores)),
			NextToken:               next,
		}
		for i := range datastores {
			resp.DatastorePropertiesList = append(resp.DatastorePropertiesList, toDatastoreProperties(&datastores[i]))
		}

		return resp, nil
	})
}
