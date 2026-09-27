package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Import modes of ImportDocumentationParts.
const (
	importMerge     = "merge"
	importOverwrite = "overwrite"
)

// importDocument is the part of an OpenAPI/Swagger file the import reads: the
// x-amazon-apigateway-documentation extension. YAML decoding also covers JSON.
type importDocument struct {
	Documentation *struct {
		Parts []importPart `yaml:"documentationParts"`
	} `yaml:"x-amazon-apigateway-documentation"`
}

type importPart struct {
	Location struct {
		Type       string `yaml:"type"`
		Path       string `yaml:"path"`
		Method     string `yaml:"method"`
		StatusCode string `yaml:"statusCode"`
		Name       string `yaml:"name"`
	} `yaml:"location"`
	Properties any `yaml:"properties"`
}

// validPart is an import entry that passed validation.
type validPart struct {
	loc   driver.DocumentationPartLocation
	props string
}

// ImportDocumentationParts reads the documentation parts of an OpenAPI file.
// merge updates parts at matching locations and adds the rest; overwrite
// replaces every existing part. Invalid entries become warnings, or fail the
// whole import under failOnWarnings.
func (m *Mock) ImportDocumentationParts(
	_ context.Context, restAPIID string, in driver.ImportDocumentationPartsInput,
) (*driver.DocumentationPartIDs, error) {
	mode, err := importMode(in.Mode)
	if err != nil {
		return nil, err
	}

	parts, warnings, err := parseImport(in.Body)
	if err != nil {
		return nil, err
	}

	if in.FailOnWarnings && len(warnings) > 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "Warnings found during import:\n\t%s", strings.Join(warnings, "\n\t"))
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if mode == importOverwrite {
		ad.docParts = make(map[string]*driver.DocumentationPart, len(parts))
	}

	ids := make([]string, 0, len(parts))

	for _, vp := range parts {
		p := ad.partAt(&vp.loc)
		if p == nil {
			p = &driver.DocumentationPart{ID: genShortID(), Location: vp.loc}
			ad.docParts[p.ID] = p
		}

		p.Properties = vp.props
		ids = append(ids, p.ID)
	}

	return &driver.DocumentationPartIDs{IDs: ids, Warnings: warnings}, nil
}

// importMode resolves the mode query parameter, defaulting to merge.
func importMode(mode string) (string, error) {
	switch mode {
	case "":
		return importMerge, nil
	case importMerge, importOverwrite:
		return mode, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'mode' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [merge, overwrite]", mode)
	}
}

// parseImport decodes the body and validates each part, turning a bad entry
// into a warning. A body that does not parse at all is a BadRequest.
func parseImport(body []byte) ([]validPart, []string, error) {
	var doc importDocument
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, nil, cerrors.Newf(cerrors.InvalidArgument, "Invalid OpenAPI input: %v", err)
	}

	if doc.Documentation == nil {
		return nil, nil, nil
	}

	var (
		parts    []validPart
		warnings []string
	)

	for i := range doc.Documentation.Parts {
		vp, err := validateImportPart(&doc.Documentation.Parts[i])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Documentation part %d skipped: %s", i, cerrors.Message(err)))
			continue
		}

		parts = append(parts, vp)
	}

	return parts, warnings, nil
}

func validateImportPart(ip *importPart) (validPart, error) {
	loc, err := canonicalLocation(&driver.DocumentationPartLocation{
		Type: ip.Location.Type, Path: ip.Location.Path, Method: ip.Location.Method,
		StatusCode: ip.Location.StatusCode, Name: ip.Location.Name,
	})
	if err != nil {
		return validPart{}, err
	}

	props, err := importProperties(ip.Properties)
	if err != nil {
		return validPart{}, err
	}

	return validPart{loc: loc, props: props}, nil
}

// importProperties renders an entry's properties as the JSON string a part
// stores. The file may carry them as an object or as an encoded string.
func importProperties(v any) (string, error) {
	if v == nil {
		return "", nullError("properties")
	}

	if s, ok := v.(string); ok {
		return s, validateProperties(s, "properties")
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument, "Invalid documentation part properties: %v", err)
	}

	return string(raw), nil
}
