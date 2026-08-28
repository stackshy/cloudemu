package apigateway

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// CreateResource adds a child resource under parentID with the given pathPart
// (a literal segment, a "{param}" placeholder, or a "{proxy+}" greedy segment).
func (m *Mock) CreateResource(_ context.Context, restAPIID, parentID, pathPart string) (*driver.Resource, error) {
	if pathPart == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "pathPart is required")
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	parent, ok := ad.resources[parentID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", parentID)
	}

	fullPath := joinPath(parent.Path, pathPart)
	if existing := findByPath(ad.resources, fullPath); existing != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"Another resource with the same parent already has this name: %s", pathPart)
	}

	res := &driver.Resource{
		ID: genID(), RestAPIID: restAPIID, ParentID: parentID,
		PathPart: pathPart, Path: fullPath, Methods: map[string]*driver.Method{},
	}
	ad.resources[res.ID] = res

	out := copyResource(res)

	return &out, nil
}

// GetResources lists every resource of a REST API.
func (m *Mock) GetResources(_ context.Context, restAPIID string) ([]driver.Resource, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := make([]driver.Resource, 0, len(ad.resources))
	for _, r := range ad.resources {
		out = append(out, copyResource(r))
	}

	return out, nil
}

// GetResource returns a single resource by id.
func (m *Mock) GetResource(_ context.Context, restAPIID, resourceID string) (*driver.Resource, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	r, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", resourceID)
	}

	out := copyResource(r)

	return &out, nil
}

// joinPath appends a child pathPart onto a parent path, normalizing slashes so
// the root ("/") yields "/pets" rather than "//pets".
func joinPath(parentPath, pathPart string) string {
	if parentPath == "/" {
		return "/" + pathPart
	}

	return parentPath + "/" + pathPart
}

// findByPath returns the resource with the given resolved path, or nil.
func findByPath(resources map[string]*driver.Resource, path string) *driver.Resource {
	for _, r := range resources {
		if r.Path == path {
			return r
		}
	}

	return nil
}

// copyResource returns a deep copy of a resource, including its methods and
// their integrations, so callers never share pointers with the stored tree.
func copyResource(r *driver.Resource) driver.Resource {
	out := *r
	out.Methods = make(map[string]*driver.Method, len(r.Methods))

	for k, mth := range r.Methods {
		cp := *mth

		if mth.Integration != nil {
			ig := *mth.Integration
			cp.Integration = &ig
		}

		out.Methods[k] = &cp
	}

	return out
}

// normalizeMethod upper-cases an HTTP method token (the tree keys are stored
// upper-cased; "ANY" is the catch-all).
func normalizeMethod(method string) string {
	return strings.ToUpper(method)
}
