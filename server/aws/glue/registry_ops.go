package glue

import (
	"context"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// registryIDJSON mirrors the SDK RegistryID wrapper.
type registryIDJSON struct {
	RegistryName string `json:"RegistryName"`
	RegistryArn  string `json:"RegistryArn"`
}

// schemaIDJSON mirrors the SDK SchemaID wrapper.
type schemaIDJSON struct {
	RegistryName string `json:"RegistryName"`
	SchemaName   string `json:"SchemaName"`
	SchemaArn    string `json:"SchemaArn"`
}

// schemaVersionNumberJSON mirrors the SDK SchemaVersionNumber wrapper.
type schemaVersionNumberJSON struct {
	LatestVersion bool  `json:"LatestVersion"`
	VersionNumber int64 `json:"VersionNumber"`
}

type registryJSON struct {
	RegistryName string `json:"RegistryName,omitempty"`
	RegistryArn  string `json:"RegistryArn,omitempty"`
	Description  string `json:"Description,omitempty"`
	Status       string `json:"Status,omitempty"`
	CreatedTime  string `json:"CreatedTime,omitempty"`
	UpdatedTime  string `json:"UpdatedTime,omitempty"`
}

func registryToWire(reg *driver.Registry) registryJSON {
	return registryJSON{
		RegistryName: reg.Name, RegistryArn: reg.ARN, Description: reg.Description, Status: reg.Status,
		CreatedTime: reg.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		UpdatedTime: reg.UpdatedTime.Format("2006-01-02T15:04:05.000Z"),
	}
}

type createRegistryRequest struct {
	RegistryName string `json:"RegistryName"`
	Description  string `json:"Description"`
}

func (h *Handler) createRegistry(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createRegistryRequest) (any, error) {
		reg, err := h.glue.CreateRegistry(ctx, driver.Registry{Name: req.RegistryName, Description: req.Description})
		if err != nil {
			return nil, err
		}

		return registryToWire(reg), nil
	})
}

type registryIDRequest struct {
	RegistryID registryIDJSON `json:"RegistryId"`
}

func (h *Handler) getRegistry(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *registryIDRequest) (any, error) {
		reg, err := h.glue.GetRegistry(ctx, req.RegistryID.RegistryName)
		if err != nil {
			return nil, err
		}

		return registryToWire(reg), nil
	})
}

type updateRegistryRequest struct {
	RegistryID  registryIDJSON `json:"RegistryId"`
	Description string         `json:"Description"`
}

func (h *Handler) updateRegistry(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateRegistryRequest) (any, error) {
		reg, err := h.glue.UpdateRegistry(ctx, req.RegistryID.RegistryName, req.Description)
		if err != nil {
			return nil, err
		}

		return registryToWire(reg), nil
	})
}

func (h *Handler) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *registryIDRequest) (any, error) {
		reg, err := h.glue.DeleteRegistry(ctx, req.RegistryID.RegistryName)
		if err != nil {
			return nil, err
		}

		return registryJSON{RegistryName: reg.Name, RegistryArn: reg.ARN, Status: "DELETING"}, nil
	})
}

type listRegistriesResponse struct {
	Registries []registryJSON `json:"Registries"`
	NextToken  string         `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) listRegistries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		regs, next, err := h.glue.ListRegistries(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]registryJSON, 0, len(regs))
		for i := range regs {
			out = append(out, registryToWire(&regs[i]))
		}

		return listRegistriesResponse{Registries: out, NextToken: next}, nil
	})
}

// --- schemas ---

type schemaJSON struct {
	RegistryName        string `json:"RegistryName,omitempty"`
	SchemaName          string `json:"SchemaName,omitempty"`
	SchemaArn           string `json:"SchemaArn,omitempty"`
	Description         string `json:"Description,omitempty"`
	DataFormat          string `json:"DataFormat,omitempty"`
	Compatibility       string `json:"Compatibility,omitempty"`
	SchemaStatus        string `json:"SchemaStatus,omitempty"`
	LatestSchemaVersion int64  `json:"LatestSchemaVersion,omitempty"`
	NextSchemaVersion   int64  `json:"NextSchemaVersion,omitempty"`
}

func schemaToWire(s *driver.Schema) schemaJSON {
	return schemaJSON{
		RegistryName: s.RegistryName, SchemaName: s.Name, SchemaArn: s.ARN, Description: s.Description,
		DataFormat: s.DataFormat, Compatibility: s.Compatibility, SchemaStatus: s.Status,
		LatestSchemaVersion: s.LatestVersion, NextSchemaVersion: s.NextVersion,
	}
}

type createSchemaRequest struct {
	RegistryID       registryIDJSON `json:"RegistryId"`
	SchemaName       string         `json:"SchemaName"`
	DataFormat       string         `json:"DataFormat"`
	Compatibility    string         `json:"Compatibility"`
	Description      string         `json:"Description"`
	SchemaDefinition string         `json:"SchemaDefinition"`
}

func (h *Handler) createSchema(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createSchemaRequest) (any, error) {
		s, err := h.glue.CreateSchema(ctx, driver.Schema{
			RegistryName: req.RegistryID.RegistryName, Name: req.SchemaName, DataFormat: req.DataFormat,
			Compatibility: req.Compatibility, Description: req.Description,
		}, req.SchemaDefinition)
		if err != nil {
			return nil, err
		}

		return schemaToWire(s), nil
	})
}

type schemaIDRequest struct {
	SchemaID schemaIDJSON `json:"SchemaId"`
}

func (h *Handler) getSchema(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *schemaIDRequest) (any, error) {
		s, err := h.glue.GetSchema(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName)
		if err != nil {
			return nil, err
		}

		return schemaToWire(s), nil
	})
}

type updateSchemaRequest struct {
	SchemaID      schemaIDJSON `json:"SchemaId"`
	Compatibility string       `json:"Compatibility"`
	Description   string       `json:"Description"`
}

type updateSchemaResponse struct {
	SchemaName   string `json:"SchemaName,omitempty"`
	SchemaArn    string `json:"SchemaArn,omitempty"`
	RegistryName string `json:"RegistryName,omitempty"`
}

func (h *Handler) updateSchema(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateSchemaRequest) (any, error) {
		s, err := h.glue.UpdateSchema(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			req.Compatibility, req.Description)
		if err != nil {
			return nil, err
		}

		return updateSchemaResponse{SchemaName: s.Name, SchemaArn: s.ARN, RegistryName: s.RegistryName}, nil
	})
}

func (h *Handler) deleteSchema(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *schemaIDRequest) (any, error) {
		s, err := h.glue.DeleteSchema(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName)
		if err != nil {
			return nil, err
		}

		return schemaJSON{SchemaName: s.Name, SchemaArn: s.ARN, SchemaStatus: "DELETING"}, nil
	})
}

type listSchemasResponse struct {
	Schemas   []schemaJSON `json:"Schemas"`
	NextToken string       `json:"NextToken,omitempty"`
}

func (h *Handler) listSchemas(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *registryIDRequest) (any, error) {
		schemas, next, err := h.glue.ListSchemas(ctx, req.RegistryID.RegistryName,
			driver.TablePagination{})
		if err != nil {
			return nil, err
		}

		out := make([]schemaJSON, 0, len(schemas))
		for i := range schemas {
			out = append(out, schemaToWire(&schemas[i]))
		}

		return listSchemasResponse{Schemas: out, NextToken: next}, nil
	})
}

// --- schema versions ---

type schemaVersionJSON struct {
	SchemaVersionID  string `json:"SchemaVersionId,omitempty"`
	SchemaArn        string `json:"SchemaArn,omitempty"`
	SchemaName       string `json:"SchemaName,omitempty"`
	RegistryName     string `json:"RegistryName,omitempty"`
	VersionNumber    int64  `json:"VersionNumber,omitempty"`
	Status           string `json:"Status,omitempty"`
	SchemaDefinition string `json:"SchemaDefinition,omitempty"`
}

func schemaVersionToWire(v *driver.SchemaVersion) schemaVersionJSON {
	return schemaVersionJSON{
		SchemaVersionID: v.VersionID, SchemaName: v.SchemaName, RegistryName: v.RegistryName,
		VersionNumber: v.VersionNumber, Status: v.Status, SchemaDefinition: v.Definition,
	}
}

type registerSchemaVersionRequest struct {
	SchemaID         schemaIDJSON `json:"SchemaId"`
	SchemaDefinition string       `json:"SchemaDefinition"`
}

type registerSchemaVersionResponse struct {
	SchemaVersionID string `json:"SchemaVersionId,omitempty"`
	VersionNumber   int64  `json:"VersionNumber,omitempty"`
	Status          string `json:"Status,omitempty"`
}

func (h *Handler) registerSchemaVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *registerSchemaVersionRequest) (any, error) {
		v, err := h.glue.RegisterSchemaVersion(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			req.SchemaDefinition)
		if err != nil {
			return nil, err
		}

		return registerSchemaVersionResponse{SchemaVersionID: v.VersionID, VersionNumber: v.VersionNumber, Status: v.Status}, nil
	})
}

type getSchemaVersionRequest struct {
	SchemaID            schemaIDJSON            `json:"SchemaId"`
	SchemaVersionID     string                  `json:"SchemaVersionId"`
	SchemaVersionNumber schemaVersionNumberJSON `json:"SchemaVersionNumber"`
}

func (h *Handler) getSchemaVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getSchemaVersionRequest) (any, error) {
		v, err := h.glue.GetSchemaVersion(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			req.SchemaVersionID, req.SchemaVersionNumber.VersionNumber)
		if err != nil {
			return nil, err
		}

		return schemaVersionToWire(v), nil
	})
}

type getSchemaByDefinitionRequest struct {
	SchemaID         schemaIDJSON `json:"SchemaId"`
	SchemaDefinition string       `json:"SchemaDefinition"`
}

func (h *Handler) getSchemaByDefinition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getSchemaByDefinitionRequest) (any, error) {
		v, err := h.glue.GetSchemaByDefinition(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			req.SchemaDefinition)
		if err != nil {
			return nil, err
		}

		return schemaVersionToWire(v), nil
	})
}

type listSchemaVersionsRequest struct {
	SchemaID   schemaIDJSON `json:"SchemaId"`
	NextToken  string       `json:"NextToken"`
	MaxResults int32        `json:"MaxResults"`
}

type listSchemaVersionsResponse struct {
	Schemas   []schemaVersionJSON `json:"Schemas"`
	NextToken string              `json:"NextToken,omitempty"`
}

func (h *Handler) listSchemaVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listSchemaVersionsRequest) (any, error) {
		vs, next, err := h.glue.ListSchemaVersions(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]schemaVersionJSON, 0, len(vs))
		for i := range vs {
			out = append(out, schemaVersionToWire(&vs[i]))
		}

		return listSchemaVersionsResponse{Schemas: out, NextToken: next}, nil
	})
}

type deleteSchemaVersionsRequest struct {
	SchemaID schemaIDJSON `json:"SchemaId"`
	Versions string       `json:"Versions"`
}

type deleteSchemaVersionsResponse struct {
	SchemaVersionErrors []schemaVersionErrorJSON `json:"SchemaVersionErrors,omitempty"`
}

type schemaVersionErrorJSON struct {
	VersionNumber int64            `json:"VersionNumber,omitempty"`
	ErrorDetails  *errorDetailJSON `json:"ErrorDetails,omitempty"`
}

func (h *Handler) deleteSchemaVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteSchemaVersionsRequest) (any, error) {
		errs, err := h.glue.DeleteSchemaVersions(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName,
			req.Versions)
		if err != nil {
			return nil, err
		}

		out := make([]schemaVersionErrorJSON, 0, len(errs))

		for i := range errs {
			e := schemaVersionErrorJSON{
				ErrorDetails: &errorDetailJSON{ErrorCode: errs[i].ErrorCode, ErrorMessage: errs[i].ErrorMessage},
			}
			// The driver carries the failing version number in Values[0]; surface
			// it so callers can tell which version failed.
			if len(errs[i].Values) > 0 {
				if n, perr := strconv.ParseInt(errs[i].Values[0], 10, 64); perr == nil {
					e.VersionNumber = n
				}
			}

			out = append(out, e)
		}

		return deleteSchemaVersionsResponse{SchemaVersionErrors: out}, nil
	})
}

type checkSchemaVersionValidityRequest struct {
	DataFormat       string `json:"DataFormat"`
	SchemaDefinition string `json:"SchemaDefinition"`
}

type checkSchemaVersionValidityResponse struct {
	Valid bool   `json:"Valid"`
	Error string `json:"Error,omitempty"`
}

func (h *Handler) checkSchemaVersionValidity(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *checkSchemaVersionValidityRequest) (any, error) {
		valid, msg := h.glue.CheckSchemaVersionValidity(ctx, req.DataFormat, req.SchemaDefinition)

		return checkSchemaVersionValidityResponse{Valid: valid, Error: msg}, nil
	})
}

type getSchemaVersionsDiffRequest struct {
	SchemaID                  schemaIDJSON            `json:"SchemaId"`
	FirstSchemaVersionNumber  schemaVersionNumberJSON `json:"FirstSchemaVersionNumber"`
	SecondSchemaVersionNumber schemaVersionNumberJSON `json:"SecondSchemaVersionNumber"`
}

type getSchemaVersionsDiffResponse struct {
	Diff string `json:"Diff,omitempty"`
}

func (h *Handler) getSchemaVersionsDiff(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getSchemaVersionsDiffRequest) (any, error) {
		diff, err := h.glue.GetSchemaVersionsDiff(ctx, req.SchemaID.RegistryName, req.SchemaID.SchemaName, "", "")
		if err != nil {
			return nil, err
		}

		return getSchemaVersionsDiffResponse{Diff: diff}, nil
	})
}
