package cloudtrail

import (
	"context"
	"net/http"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

type importSourceJSON struct {
	S3 *struct {
		S3LocationURI         string `json:"S3LocationUri"`
		S3BucketRegion        string `json:"S3BucketRegion"`
		S3BucketAccessRoleArn string `json:"S3BucketAccessRoleArn"`
	} `json:"S3"`
}

type startImportRequest struct {
	ImportID       string            `json:"ImportId"`
	Destinations   []string          `json:"Destinations"`
	ImportSource   *importSourceJSON `json:"ImportSource"`
	StartEventTime *float64          `json:"StartEventTime"`
	EndEventTime   *float64          `json:"EndEventTime"`
}

func importToWire(imp *ctdriver.Import) any {
	return struct {
		ImportID         string   `json:"ImportId,omitempty"`
		ImportStatus     string   `json:"ImportStatus,omitempty"`
		Destinations     []string `json:"Destinations,omitempty"`
		CreatedTimestamp *float64 `json:"CreatedTimestamp,omitempty"`
		UpdatedTimestamp *float64 `json:"UpdatedTimestamp,omitempty"`
	}{
		ImportID: imp.ID, ImportStatus: imp.Status, Destinations: imp.Destinations,
		CreatedTimestamp: epochOrNil(imp.CreatedAt), UpdatedTimestamp: epochOrNil(imp.UpdatedAt),
	}
}

func (h *Handler) startImport(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startImportRequest) (any, error) {
		in := ctdriver.Import{ID: req.ImportID, Destinations: req.Destinations}
		if req.ImportSource != nil && req.ImportSource.S3 != nil {
			in.S3LocationURI = req.ImportSource.S3.S3LocationURI
			in.S3BucketRegion = req.ImportSource.S3.S3BucketRegion
			in.S3BucketAccessRoleARN = req.ImportSource.S3.S3BucketAccessRoleArn
		}

		imp, err := h.ct.StartImport(ctx, in)
		if err != nil {
			return nil, err
		}

		return importToWire(imp), nil
	})
}

func (h *Handler) getImport(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ImportID string `json:"ImportId"`
	},
	) (any, error) {
		imp, err := h.ct.GetImport(ctx, req.ImportID)
		if err != nil {
			return nil, err
		}

		return importToWire(imp), nil
	})
}

func (h *Handler) stopImport(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ImportID string `json:"ImportId"`
	},
	) (any, error) {
		imp, err := h.ct.StopImport(ctx, req.ImportID)
		if err != nil {
			return nil, err
		}

		return importToWire(imp), nil
	})
}

func (h *Handler) listImports(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		Destination  string `json:"Destination"`
		ImportStatus string `json:"ImportStatus"`
		NextToken    string `json:"NextToken"`
		MaxResults   int32  `json:"MaxResults"`
	},
	) (any, error) {
		imports, next, err := h.ct.ListImports(ctx, req.Destination, req.ImportStatus, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		type item struct {
			ImportID     string   `json:"ImportId,omitempty"`
			ImportStatus string   `json:"ImportStatus,omitempty"`
			Destinations []string `json:"Destinations,omitempty"`
		}

		list := make([]item, 0, len(imports))
		for i := range imports {
			list = append(list, item{
				ImportID: imports[i].ID, ImportStatus: imports[i].Status, Destinations: imports[i].Destinations,
			})
		}

		return struct {
			Imports   []item `json:"Imports"`
			NextToken string `json:"NextToken,omitempty"`
		}{Imports: list, NextToken: next}, nil
	})
}

//nolint:dupl // failure-list shape mirrors searchSampleQueries but returns a distinct wire type.
func (h *Handler) listImportFailures(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *struct {
		ImportID   string `json:"ImportId"`
		NextToken  string `json:"NextToken"`
		MaxResults int32  `json:"MaxResults"`
	},
	) (any, error) {
		failures, next, err := h.ct.ListImportFailures(ctx, req.ImportID, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		type item struct {
			Location     string `json:"Location,omitempty"`
			Status       string `json:"Status,omitempty"`
			ErrorType    string `json:"ErrorType,omitempty"`
			ErrorMessage string `json:"ErrorMessage,omitempty"`
		}

		list := make([]item, 0, len(failures))
		for i := range failures {
			list = append(list, item{
				Location: failures[i].Location, Status: failures[i].Status,
				ErrorType: failures[i].ErrorType, ErrorMessage: failures[i].ErrorMessage,
			})
		}

		return struct {
			Failures  []item `json:"Failures"`
			NextToken string `json:"NextToken,omitempty"`
		}{Failures: list, NextToken: next}, nil
	})
}
