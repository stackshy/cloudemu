package apigateway_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

func newDocAPI(t *testing.T, m *apigateway.Mock) string {
	t.Helper()

	api, err := m.CreateRestAPI(ctx(), &driver.CreateRestAPIInput{Name: "docs"})
	if err != nil {
		t.Fatalf("CreateRestAPI: %v", err)
	}

	return api.ID
}

func mustPart(t *testing.T, m *apigateway.Mock, apiID string, loc driver.DocumentationPartLocation, props string) *driver.DocumentationPart {
	t.Helper()

	p, err := m.CreateDocumentationPart(ctx(), apiID, &driver.CreateDocumentationPartInput{Location: loc, Properties: props})
	if err != nil {
		t.Fatalf("CreateDocumentationPart(%+v): %v", loc, err)
	}

	return p
}

func TestDocumentationPartLocationDefaults(t *testing.T) {
	m := newMock(t)
	apiID := newDocAPI(t, m)

	tests := []struct {
		in   driver.DocumentationPartLocation
		want driver.DocumentationPartLocation
	}{
		{driver.DocumentationPartLocation{Type: "API"}, driver.DocumentationPartLocation{Type: "API"}},
		{driver.DocumentationPartLocation{Type: "RESOURCE"}, driver.DocumentationPartLocation{Type: "RESOURCE", Path: "/"}},
		{
			driver.DocumentationPartLocation{Type: "METHOD", Path: "/pets"},
			driver.DocumentationPartLocation{Type: "METHOD", Path: "/pets", Method: "*"},
		},
		{
			driver.DocumentationPartLocation{Type: "RESPONSE", StatusCode: "200"},
			driver.DocumentationPartLocation{Type: "RESPONSE", Path: "/", Method: "*", StatusCode: "200"},
		},
		{
			driver.DocumentationPartLocation{Type: "RESPONSE_HEADER", Name: "Content-Type"},
			driver.DocumentationPartLocation{Type: "RESPONSE_HEADER", Path: "/", Method: "*", StatusCode: "*", Name: "Content-Type"},
		},
		{driver.DocumentationPartLocation{Type: "MODEL", Name: "Pet"}, driver.DocumentationPartLocation{Type: "MODEL", Name: "Pet"}},
	}

	for _, tc := range tests {
		p := mustPart(t, m, apiID, tc.in, `{"description":"x"}`)
		if p.Location != tc.want {
			t.Errorf("location %+v stored as %+v, want %+v", tc.in, p.Location, tc.want)
		}

		if len(p.ID) != 6 {
			t.Errorf("part id %q is not 6 characters", p.ID)
		}
	}
}

func TestDocumentationPartValidation(t *testing.T) {
	m := newMock(t)
	apiID := newDocAPI(t, m)

	bad := []struct {
		name  string
		loc   driver.DocumentationPartLocation
		props string
	}{
		{"missing type", driver.DocumentationPartLocation{}, `{}`},
		{"unknown type", driver.DocumentationPartLocation{Type: "WIDGET"}, `{}`},
		{"path on API", driver.DocumentationPartLocation{Type: "API", Path: "/"}, `{}`},
		{"method on RESOURCE", driver.DocumentationPartLocation{Type: "RESOURCE", Method: "GET"}, `{}`},
		{"name on METHOD", driver.DocumentationPartLocation{Type: "METHOD", Name: "x"}, `{}`},
		{"statusCode on METHOD", driver.DocumentationPartLocation{Type: "METHOD", StatusCode: "200"}, `{}`},
		{"missing name on MODEL", driver.DocumentationPartLocation{Type: "MODEL"}, `{}`},
		{"missing name on QUERY_PARAMETER", driver.DocumentationPartLocation{Type: "QUERY_PARAMETER"}, `{}`},
		{"bad status code", driver.DocumentationPartLocation{Type: "RESPONSE", StatusCode: "700"}, `{}`},
		{"relative path", driver.DocumentationPartLocation{Type: "RESOURCE", Path: "pets"}, `{}`},
		{"missing properties", driver.DocumentationPartLocation{Type: "API"}, ``},
		{"invalid properties", driver.DocumentationPartLocation{Type: "API"}, `{not json`},
	}

	for _, tc := range bad {
		_, err := m.CreateDocumentationPart(ctx(), apiID, &driver.CreateDocumentationPartInput{Location: tc.loc, Properties: tc.props})
		if !errors.IsInvalidArgument(err) {
			t.Errorf("%s: got %v, want BadRequest", tc.name, err)
		}
	}

	_, err := m.CreateDocumentationPart(ctx(), "nosuch", &driver.CreateDocumentationPartInput{
		Location: driver.DocumentationPartLocation{Type: "API"}, Properties: `{}`,
	})
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown REST API = %v, want NotFound", err)
	}
}

func TestDocumentationPartDuplicateLocation(t *testing.T) {
	m := newMock(t)
	apiID := newDocAPI(t, m)

	mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "API"}, `{"a":1}`)

	_, err := m.CreateDocumentationPart(ctx(), apiID, &driver.CreateDocumentationPartInput{
		Location: driver.DocumentationPartLocation{Type: "API"}, Properties: `{"a":2}`,
	})
	assertMessage(t, err, errors.IsAlreadyExists, "Documentation part already exists for the specified location: type 'API'.")

	// An omitted field equals its default, so these two locations collide.
	mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "METHOD", Path: "/pets"}, `{}`)

	_, err = m.CreateDocumentationPart(ctx(), apiID, &driver.CreateDocumentationPartInput{
		Location: driver.DocumentationPartLocation{Type: "METHOD", Path: "/pets", Method: "*"}, Properties: `{}`,
	})
	assertMessage(t, err, errors.IsAlreadyExists,
		"Documentation part already exists for the specified location: type 'METHOD', path '/pets', method '*'.")
}

func TestDocumentationPartLifecycleAndFilters(t *testing.T) {
	m := newMock(t)
	apiID := newDocAPI(t, m)

	api := mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "API"}, `{"info":{"description":"d"}}`)
	mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "METHOD", Path: "/pets", Method: "GET"}, `{"summary":"s"}`)
	mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "QUERY_PARAMETER", Path: "/pets", Method: "GET", Name: "page"}, `{}`)
	mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "MODEL", Name: "PetModel"}, `{"description":"m"}`)

	list := func(in *driver.GetDocumentationPartsInput) []driver.DocumentationPart {
		t.Helper()

		page, err := m.GetDocumentationParts(ctx(), apiID, in)
		if err != nil {
			t.Fatalf("GetDocumentationParts(%+v): %v", in, err)
		}

		return page.Items
	}

	if n := len(list(&driver.GetDocumentationPartsInput{})); n != 4 {
		t.Fatalf("all parts = %d, want 4", n)
	}

	if got := list(&driver.GetDocumentationPartsInput{Type: "METHOD"}); len(got) != 1 || got[0].Location.Method != "GET" {
		t.Fatalf("type=METHOD = %+v", got)
	}

	if got := list(&driver.GetDocumentationPartsInput{Path: "/pets"}); len(got) != 2 {
		t.Fatalf("path=/pets = %+v", got)
	}

	if got := list(&driver.GetDocumentationPartsInput{NameQuery: "pet"}); len(got) != 1 || got[0].Location.Name != "PetModel" {
		t.Fatalf("name=pet = %+v", got)
	}

	if got := list(&driver.GetDocumentationPartsInput{LocationStatus: "UNDOCUMENTED"}); len(got) != 1 || got[0].Location.Name != "page" {
		t.Fatalf("locationStatus=UNDOCUMENTED = %+v", got)
	}

	if got := list(&driver.GetDocumentationPartsInput{LocationStatus: "DOCUMENTED"}); len(got) != 3 {
		t.Fatalf("locationStatus=DOCUMENTED = %+v", got)
	}

	if _, err := m.GetDocumentationParts(ctx(), apiID, &driver.GetDocumentationPartsInput{Type: "BOGUS"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad type filter = %v, want BadRequest", err)
	}

	page, err := m.GetDocumentationParts(ctx(), apiID, &driver.GetDocumentationPartsInput{PageInput: driver.PageInput{Limit: 3}})
	if err != nil || len(page.Items) != 3 || page.Position == "" {
		t.Fatalf("paged parts = %+v, %v", page, err)
	}

	upd, err := m.UpdateDocumentationPart(ctx(), apiID, api.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/properties", Value: `{"info":{"description":"new"}}`},
	})
	if err != nil || upd.Properties != `{"info":{"description":"new"}}` {
		t.Fatalf("UpdateDocumentationPart = %+v, %v", upd, err)
	}

	_, err = m.UpdateDocumentationPart(ctx(), apiID, api.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/properties", Value: `{broken`},
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("invalid properties patch = %v, want BadRequest", err)
	}

	_, err = m.UpdateDocumentationPart(ctx(), apiID, api.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/location/type", Value: "MODEL"},
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("location patch = %v, want BadRequest", err)
	}

	if err := m.DeleteDocumentationPart(ctx(), apiID, api.ID); err != nil {
		t.Fatalf("DeleteDocumentationPart: %v", err)
	}

	_, err = m.GetDocumentationPart(ctx(), apiID, api.ID)
	assertMessage(t, err, errors.IsNotFound, "Invalid Documentation part identifier specified")
}

const importDoc = `{
  "swagger": "2.0",
  "info": {"title": "docs", "version": "1"},
  "paths": {},
  "x-amazon-apigateway-documentation": {
    "version": "1.0.0",
    "documentationParts": [
      {"location": {"type": "API"}, "properties": {"description": "imported"}},
      {"location": {"type": "METHOD", "path": "/pets", "method": "GET"}, "properties": {"summary": "list"}},
      {"location": {"type": "RESOURCE", "method": "GET"}, "properties": {"summary": "bad"}}
    ]
  }
}`

func TestImportDocumentationPartsMergeAndOverwrite(t *testing.T) {
	m := newMock(t)
	apiID := newDocAPI(t, m)

	existingAPI := mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "API"}, `{"description":"old"}`)
	keep := mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "MODEL", Name: "Pet"}, `{"description":"keep"}`)

	_, err := m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{
		Body: []byte(importDoc), FailOnWarnings: true,
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("failOnWarnings import = %v, want BadRequest", err)
	}

	res, err := m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{Body: []byte(importDoc)})
	if err != nil {
		t.Fatalf("merge import: %v", err)
	}

	if len(res.IDs) != 2 || len(res.Warnings) != 1 {
		t.Fatalf("merge import = %+v, want 2 ids and 1 warning", res)
	}

	// Merge keeps the part id at a matching location and updates its content.
	got, err := m.GetDocumentationPart(ctx(), apiID, existingAPI.ID)
	if err != nil || got.Properties != `{"description":"imported"}` {
		t.Fatalf("merged API part = %+v, %v", got, err)
	}

	if _, err := m.GetDocumentationPart(ctx(), apiID, keep.ID); err != nil {
		t.Fatalf("merge dropped an unrelated part: %v", err)
	}

	res, err = m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{Body: []byte(importDoc), Mode: "overwrite"})
	if err != nil || len(res.IDs) != 2 {
		t.Fatalf("overwrite import = %+v, %v", res, err)
	}

	if _, err := m.GetDocumentationPart(ctx(), apiID, keep.ID); !errors.IsNotFound(err) {
		t.Fatalf("overwrite kept a part not in the import: %v", err)
	}

	page, _ := m.GetDocumentationParts(ctx(), apiID, &driver.GetDocumentationPartsInput{})
	if len(page.Items) != 2 {
		t.Fatalf("parts after overwrite = %+v", page.Items)
	}

	yamlDoc := "swagger: '2.0'\nx-amazon-apigateway-documentation:\n  documentationParts:\n" +
		"    - location: {type: MODEL, name: Cat}\n      properties: {description: cat}\n"

	res, err = m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{Body: []byte(yamlDoc)})
	if err != nil || len(res.IDs) != 1 {
		t.Fatalf("yaml import = %+v, %v", res, err)
	}

	if _, err := m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{
		Body: []byte(importDoc), Mode: "replace",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad mode = %v, want BadRequest", err)
	}

	if _, err := m.ImportDocumentationParts(ctx(), apiID, driver.ImportDocumentationPartsInput{Body: []byte("{")}); !errors.IsInvalidArgument(err) {
		t.Fatalf("unparsable body = %v, want BadRequest", err)
	}
}

func TestDocumentationVersionLifecycle(t *testing.T) {
	m := newMock(t)
	apiID, _, _ := deployProxyAPI(t, m, "hello", "GET", lambdaURI)

	part := mustPart(t, m, apiID, driver.DocumentationPartLocation{Type: "API"}, `{"description":"v1"}`)

	_, err := m.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("missing version = %v, want BadRequest", err)
	}

	_, err = m.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{Version: "1", StageName: "nosuch"})
	if !errors.IsNotFound(err) {
		t.Fatalf("unknown stage = %v, want NotFound", err)
	}

	v, err := m.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{
		Version: "1.0", Description: "first", StageName: "prod",
	})
	if err != nil || v.Version != "1.0" || v.Description != "first" {
		t.Fatalf("CreateDocumentationVersion = %+v, %v", v, err)
	}

	st, _ := m.GetStage(ctx(), apiID, "prod")
	if st.DocumentationVersion != "1.0" {
		t.Fatalf("stage documentationVersion = %q, want 1.0", st.DocumentationVersion)
	}

	_, err = m.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{Version: "1.0"})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate version = %v, want Conflict", err)
	}

	if _, err := m.UpdateDocumentationPart(ctx(), apiID, part.ID, []driver.PatchOperation{
		{Op: "replace", Path: "/properties", Value: `{"description":"v2"}`},
	}); err != nil {
		t.Fatalf("UpdateDocumentationPart: %v", err)
	}

	if _, err := m.CreateDocumentationVersion(ctx(), apiID, driver.CreateDocumentationVersionInput{Version: "2.0"}); err != nil {
		t.Fatalf("second version: %v", err)
	}

	page, err := m.GetDocumentationVersions(ctx(), apiID, driver.PageInput{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("GetDocumentationVersions = %+v, %v", page, err)
	}

	upd, err := m.UpdateDocumentationVersion(ctx(), apiID, "2.0", []driver.PatchOperation{
		{Op: "replace", Path: "/description", Value: "second"},
	})
	if err != nil || upd.Description != "second" {
		t.Fatalf("UpdateDocumentationVersion = %+v, %v", upd, err)
	}

	st, err = m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/documentationVersion", Value: "2.0"},
	})
	if err != nil || st.DocumentationVersion != "2.0" {
		t.Fatalf("UpdateStage documentationVersion = %+v, %v", st, err)
	}

	_, err = m.UpdateStage(ctx(), apiID, "prod", []driver.PatchOperation{
		{Op: "replace", Path: "/documentationVersion", Value: "9.9"},
	})
	assertMessage(t, err, errors.IsNotFound, "Invalid Documentation version identifier specified")

	if err := m.DeleteDocumentationVersion(ctx(), apiID, "2.0"); !errors.IsInvalidArgument(err) {
		t.Fatalf("deleting a version a stage uses = %v, want BadRequest", err)
	}

	if err := m.DeleteDocumentationVersion(ctx(), apiID, "1.0"); err != nil {
		t.Fatalf("DeleteDocumentationVersion: %v", err)
	}

	_, err = m.GetDocumentationVersion(ctx(), apiID, "1.0")
	assertMessage(t, err, errors.IsNotFound, "Invalid Documentation version identifier specified")

	// CreateStage validates documentationVersion too.
	_, err = m.CreateStage(ctx(), apiID, driver.CreateStageInput{
		StageName: "beta", DeploymentID: st.DeploymentID, DocumentationVersion: "nope",
	})
	if !errors.IsNotFound(err) {
		t.Fatalf("CreateStage with unknown documentationVersion = %v, want NotFound", err)
	}
}
