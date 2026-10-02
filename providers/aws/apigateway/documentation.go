package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	msgDocPartNotFound = "Invalid Documentation part identifier specified"
	msgDocPartExists   = "Documentation part already exists for the specified location: %s."

	// wildcard is the default method and statusCode of a location: any value.
	wildcard = "*"

	locationDocumented   = "DOCUMENTED"
	locationUndocumented = "UNDOCUMENTED"

	pathProperties = "/properties"
)

// statusCodePattern is the DocumentationPartLocationStatusCode constraint.
var statusCodePattern = regexp.MustCompile(`^([1-5]\d\d|\*|\s*)$`)

// docLocationRule is which optional location fields a documentation type
// accepts, and whether it needs a name.
type docLocationRule struct {
	path, method, statusCode, name, nameRequired bool
}

// locationRule returns the rule for a documentation type, per the API Gateway
// "valid location fields" table.
func locationRule(docType string) (docLocationRule, bool) {
	switch docType {
	case driver.DocTypeAPI:
		return docLocationRule{}, true
	case driver.DocTypeAuthorizer, driver.DocTypeModel:
		return docLocationRule{name: true, nameRequired: true}, true
	case driver.DocTypeResource:
		return docLocationRule{path: true}, true
	case driver.DocTypeMethod:
		return docLocationRule{path: true, method: true}, true
	case driver.DocTypeRequestBody:
		return docLocationRule{path: true, method: true, name: true}, true
	case driver.DocTypePathParameter, driver.DocTypeQueryParameter, driver.DocTypeRequestHeader:
		return docLocationRule{path: true, method: true, name: true, nameRequired: true}, true
	case driver.DocTypeResponse, driver.DocTypeResponseBody:
		return docLocationRule{path: true, method: true, statusCode: true}, true
	case driver.DocTypeResponseHeader:
		return docLocationRule{path: true, method: true, statusCode: true, name: true, nameRequired: true}, true
	default:
		return docLocationRule{}, false
	}
}

// docTypes is the DocumentationPartType enum in model order.
func docTypes() []string {
	return []string{
		driver.DocTypeAPI, driver.DocTypeAuthorizer, driver.DocTypeModel, driver.DocTypeResource,
		driver.DocTypeMethod, driver.DocTypePathParameter, driver.DocTypeQueryParameter,
		driver.DocTypeRequestHeader, driver.DocTypeRequestBody, driver.DocTypeResponse,
		driver.DocTypeResponseHeader, driver.DocTypeResponseBody,
	}
}

func enumError(field, value string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [%s]", value, field, strings.Join(docTypes(), ", "))
}

func nullError(field string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"1 validation error detected: Value null at '%s' failed to satisfy constraint: Member must not be null", field)
}

// canonicalLocation validates loc against its type and fills the defaults a
// real read returns: path "/", method and statusCode "*".
func canonicalLocation(in *driver.DocumentationPartLocation) (driver.DocumentationPartLocation, error) {
	loc := *in
	if loc.Type == "" {
		return loc, nullError("createDocumentationPartInput.location.type")
	}

	rule, ok := locationRule(loc.Type)
	if !ok {
		return loc, enumError("createDocumentationPartInput.location.type", loc.Type)
	}

	if err := checkLocationFields(&loc, rule); err != nil {
		return loc, err
	}

	fillDefault(&loc.Path, rule.path, "/")
	fillDefault(&loc.Method, rule.method, wildcard)
	fillDefault(&loc.StatusCode, rule.statusCode, wildcard)

	return loc, nil
}

// checkLocationFields rejects a field the type does not accept, a missing
// required name, a relative path and a malformed status code.
func checkLocationFields(loc *driver.DocumentationPartLocation, rule docLocationRule) error {
	for _, f := range []struct {
		name  string
		set   bool
		valid bool
	}{
		{"path", loc.Path != "", rule.path},
		{"method", loc.Method != "", rule.method},
		{"statusCode", loc.StatusCode != "", rule.statusCode},
		{"name", loc.Name != "", rule.name},
	} {
		if f.set && !f.valid {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid documentation part location: '%s' is not a valid field for type '%s'", f.name, loc.Type)
		}
	}

	if rule.nameRequired && loc.Name == "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid documentation part location: 'name' is required for type '%s'", loc.Type)
	}

	if loc.Path != "" && !strings.HasPrefix(loc.Path, "/") {
		return cerrors.Newf(cerrors.InvalidArgument, "Invalid documentation part location: invalid path '%s'", loc.Path)
	}

	if !statusCodePattern.MatchString(loc.StatusCode) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid documentation part location: invalid statusCode '%s'", loc.StatusCode)
	}

	return nil
}

func fillDefault(field *string, applies bool, def string) {
	if applies && strings.TrimSpace(*field) == "" {
		*field = def
	}
}

// describeLocation renders a location the way the duplicate-location error
// names it: type 'METHOD', path '/pets', method 'GET'.
func describeLocation(loc *driver.DocumentationPartLocation) string {
	parts := []string{fmt.Sprintf("type '%s'", loc.Type)}

	for _, f := range [][2]string{
		{"path", loc.Path}, {"method", loc.Method}, {"statusCode", loc.StatusCode}, {"name", loc.Name},
	} {
		if f[1] != "" {
			parts = append(parts, fmt.Sprintf("%s '%s'", f[0], f[1]))
		}
	}

	return strings.Join(parts, ", ")
}

// validateProperties checks properties is present and valid JSON.
func validateProperties(props, field string) error {
	if props == "" {
		return nullError(field)
	}

	if !json.Valid([]byte(props)) {
		return cerrors.New(cerrors.InvalidArgument, "Invalid documentation part properties: must be valid JSON")
	}

	return nil
}

// isDocumented reports whether a part carries any content: an empty object or
// null counts as undocumented.
func isDocumented(props string) bool {
	var v any
	if err := json.Unmarshal([]byte(props), &v); err != nil || v == nil {
		return false
	}

	if obj, ok := v.(map[string]any); ok {
		return len(obj) > 0
	}

	return true
}

// partAt returns the part stored at a canonical location, if any. The caller
// holds ad.mu.
func (ad *apiData) partAt(loc *driver.DocumentationPartLocation) *driver.DocumentationPart {
	for _, p := range ad.docParts {
		if p.Location == *loc {
			return p
		}
	}

	return nil
}

// CreateDocumentationPart adds a part at a location no other part occupies.
func (m *Mock) CreateDocumentationPart(
	_ context.Context, restAPIID string, in *driver.CreateDocumentationPartInput,
) (*driver.DocumentationPart, error) {
	loc, err := canonicalLocation(&in.Location)
	if err != nil {
		return nil, err
	}

	if propErr := validateProperties(in.Properties, "createDocumentationPartInput.properties"); propErr != nil {
		return nil, propErr
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if ad.partAt(&loc) != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, msgDocPartExists, describeLocation(&loc))
	}

	p := &driver.DocumentationPart{ID: genShortID(), Location: loc, Properties: in.Properties}
	ad.docParts[p.ID] = p

	out := *p

	return &out, nil
}

// GetDocumentationPart returns one part.
func (m *Mock) GetDocumentationPart(_ context.Context, restAPIID, partID string) (*driver.DocumentationPart, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	p, ok := ad.docParts[partID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDocPartNotFound)
	}

	out := *p

	return &out, nil
}

// GetDocumentationParts lists the parts matching the type, path, name and
// location-status filters, one page at a time.
func (m *Mock) GetDocumentationParts(
	_ context.Context, restAPIID string, in *driver.GetDocumentationPartsInput,
) (*driver.DocumentationPartPage, error) {
	if err := validatePartFilters(in); err != nil {
		return nil, err
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()

	all := make([]driver.DocumentationPart, 0, len(ad.docParts))

	for _, p := range ad.docParts {
		if partMatches(p, in) {
			all = append(all, *p)
		}
	}

	ad.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	items, next, err := pageOf(all, in.PageInput)
	if err != nil {
		return nil, err
	}

	return &driver.DocumentationPartPage{Items: items, Position: next}, nil
}

func validatePartFilters(in *driver.GetDocumentationPartsInput) error {
	if _, ok := locationRule(in.Type); in.Type != "" && !ok {
		return enumError("type", in.Type)
	}

	switch in.LocationStatus {
	case "", locationDocumented, locationUndocumented:
		return nil
	default:
		return cerrors.Newf(cerrors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'locationStatus' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [DOCUMENTED, UNDOCUMENTED]", in.LocationStatus)
	}
}

func partMatches(p *driver.DocumentationPart, in *driver.GetDocumentationPartsInput) bool {
	if in.Type != "" && p.Location.Type != in.Type {
		return false
	}

	if in.Path != "" && p.Location.Path != in.Path {
		return false
	}

	if in.NameQuery != "" && !strings.Contains(strings.ToLower(p.Location.Name), strings.ToLower(in.NameQuery)) {
		return false
	}

	switch in.LocationStatus {
	case locationDocumented:
		return isDocumented(p.Properties)
	case locationUndocumented:
		return !isDocumented(p.Properties)
	default:
		return true
	}
}

// UpdateDocumentationPart applies a patch document; only /properties can
// change, since the location is the part's identity.
func (m *Mock) UpdateDocumentationPart(
	_ context.Context, restAPIID, partID string, ops []driver.PatchOperation,
) (*driver.DocumentationPart, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	p, ok := ad.docParts[partID]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgDocPartNotFound)
	}

	props := p.Properties

	for _, op := range ops {
		if op.Path != pathProperties || op.Op != opReplace {
			return nil, invalidPatchPath(op, pathProperties)
		}

		if err := validateProperties(op.Value, "updateDocumentationPartInput.properties"); err != nil {
			return nil, err
		}

		props = op.Value
	}

	p.Properties = props
	out := *p

	return &out, nil
}

// DeleteDocumentationPart removes a part. Published documentation versions
// keep their own copy.
func (m *Mock) DeleteDocumentationPart(_ context.Context, restAPIID, partID string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.docParts[partID]; !ok {
		return cerrors.New(cerrors.NotFound, msgDocPartNotFound)
	}

	delete(ad.docParts, partID)

	return nil
}
