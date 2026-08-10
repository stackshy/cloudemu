package glue

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// errInvalidClassifier is returned when a CreateClassifier/UpdateClassifier
// request carries none of the four classifier sub-objects.
var errInvalidClassifier = &driver.APIError{
	Exception: driver.ExInvalidInput,
	Err:       cerrors.New(cerrors.InvalidArgument, "exactly one classifier definition must be provided"),
}

// Glue classifiers are a tagged union of Grok/Json/Csv/Xml on the wire. The
// emulator stores whichever sub-object is present as an opaque definition keyed
// by its kind, preserving the field the caller sent.

type classifierUnionJSON struct {
	GrokClassifier map[string]any `json:"GrokClassifier,omitempty"`
	XMLClassifier  map[string]any `json:"XMLClassifier,omitempty"`
	JSONClassifier map[string]any `json:"JsonClassifier,omitempty"`
	CsvClassifier  map[string]any `json:"CsvClassifier,omitempty"`
}

func classifierFromUnion(u classifierUnionJSON) (driver.Classifier, bool) {
	// Exactly one of the four sub-objects must be present.
	present := 0

	for _, c := range []map[string]any{u.GrokClassifier, u.XMLClassifier, u.JSONClassifier, u.CsvClassifier} {
		if c != nil {
			present++
		}
	}

	if present != 1 {
		return driver.Classifier{}, false
	}

	switch {
	case u.GrokClassifier != nil:
		return driver.Classifier{Name: strAny(u.GrokClassifier["Name"]), Kind: "Grok", Definition: u.GrokClassifier}, true
	case u.XMLClassifier != nil:
		return driver.Classifier{Name: strAny(u.XMLClassifier["Name"]), Kind: "XML", Definition: u.XMLClassifier}, true
	case u.JSONClassifier != nil:
		return driver.Classifier{Name: strAny(u.JSONClassifier["Name"]), Kind: "Json", Definition: u.JSONClassifier}, true
	case u.CsvClassifier != nil:
		return driver.Classifier{Name: strAny(u.CsvClassifier["Name"]), Kind: "Csv", Definition: u.CsvClassifier}, true
	default:
		return driver.Classifier{}, false
	}
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}

type classifierJSON struct {
	GrokClassifier map[string]any `json:"GrokClassifier,omitempty"`
	XMLClassifier  map[string]any `json:"XMLClassifier,omitempty"`
	JSONClassifier map[string]any `json:"JsonClassifier,omitempty"`
	CsvClassifier  map[string]any `json:"CsvClassifier,omitempty"`
}

func classifierToWire(c *driver.Classifier) classifierJSON {
	switch c.Kind {
	case "Grok":
		return classifierJSON{GrokClassifier: c.Definition}
	case "XML":
		return classifierJSON{XMLClassifier: c.Definition}
	case "Json":
		return classifierJSON{JSONClassifier: c.Definition}
	case "Csv":
		return classifierJSON{CsvClassifier: c.Definition}
	default:
		return classifierJSON{}
	}
}

func (h *Handler) createClassifier(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *classifierUnionJSON) (any, error) {
		c, ok := classifierFromUnion(*req)
		if !ok {
			return nil, errInvalidClassifier
		}

		if err := h.glue.CreateClassifier(ctx, c); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type classifierNameRequest struct {
	Name string `json:"Name"`
}

type getClassifierResponse struct {
	Classifier classifierJSON `json:"Classifier"`
}

func (h *Handler) getClassifier(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *classifierNameRequest) (any, error) {
		c, err := h.glue.GetClassifier(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getClassifierResponse{Classifier: classifierToWire(c)}, nil
	})
}

func (h *Handler) updateClassifier(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *classifierUnionJSON) (any, error) {
		c, ok := classifierFromUnion(*req)
		if !ok {
			return nil, errInvalidClassifier
		}

		if err := h.glue.UpdateClassifier(ctx, c); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteClassifier(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *classifierNameRequest) (any, error) {
		if err := h.glue.DeleteClassifier(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getClassifiersResponse struct {
	Classifiers []classifierJSON `json:"Classifiers"`
	NextToken   string           `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getClassifiers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		cs, next, err := h.glue.GetClassifiers(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]classifierJSON, 0, len(cs))
		for i := range cs {
			out = append(out, classifierToWire(&cs[i]))
		}

		return getClassifiersResponse{Classifiers: out, NextToken: next}, nil
	})
}
