package apigateway

import (
	"context"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// opReplace, opAdd and opRemove are the JSON Patch ops API Gateway's
// patchOperations documents use.
const (
	opReplace = "replace"
	opAdd     = "add"
	opRemove  = "remove"
)

// pathDescription is the JSON Pointer for the /description field, shared by the
// RestApi, Stage and Deployment patch appliers.
const pathDescription = "/description"

// UpdateRestAPI applies a patchOperations document to a REST API.
func (m *Mock) UpdateRestAPI(_ context.Context, id string, ops []driver.PatchOperation) (*driver.RestAPI, error) {
	ad, err := m.getAPI(id)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	for _, op := range ops {
		applyRestAPIPatch(&ad.api, op)
	}

	out := copyAPI(&ad.api)

	return &out, nil
}

// applyRestAPIPatch applies one patch op to a RestAPI, ignoring paths the model
// does not track (matching how a client only patches fields it manages).
func applyRestAPIPatch(api *driver.RestAPI, op driver.PatchOperation) {
	switch op.Path {
	case "/name":
		api.Name = op.Value
	case pathDescription:
		api.Description = op.Value
	case "/apiKeySource":
		api.APIKeySource = op.Value
	case "/policy":
		api.Policy = op.Value
	case "/disableExecuteApiEndpoint":
		api.DisableExecuteAPIEndpoint = parseBool(op.Value)
	case "/minimumCompressionSize":
		api.MinimumCompressionSize = parseCompressionSize(op)
	default:
		applyRestAPIListPatch(api, op)
	}
}

// applyRestAPIListPatch handles the slice-valued RestAPI patch paths
// (binaryMediaTypes add/remove and endpointConfiguration/types/<i> replace).
func applyRestAPIListPatch(api *driver.RestAPI, op driver.PatchOperation) {
	switch {
	case strings.HasPrefix(op.Path, "/binaryMediaTypes/"):
		mediaType := unescapePointer(strings.TrimPrefix(op.Path, "/binaryMediaTypes/"))
		api.BinaryMediaTypes = patchStringSlice(api.BinaryMediaTypes, op.Op, mediaType)
	case strings.HasPrefix(op.Path, "/endpointConfiguration/types/"):
		if op.Op == opReplace {
			idx, err := strconv.Atoi(strings.TrimPrefix(op.Path, "/endpointConfiguration/types/"))
			if err == nil && idx >= 0 && idx < len(api.EndpointConfigurationTypes) {
				api.EndpointConfigurationTypes[idx] = op.Value
			}
		}
	}
}

// parseCompressionSize maps a /minimumCompressionSize op to the stored pointer:
// remove (or a negative sentinel) disables compression (nil); a non-negative
// value enables it.
func parseCompressionSize(op driver.PatchOperation) *int {
	if op.Op == opRemove {
		return nil
	}

	n, err := strconv.Atoi(op.Value)
	if err != nil || n < 0 {
		return nil
	}

	return &n
}

// UpdateResource applies a patch document to a resource: /pathPart renames it
// and /parentId moves it, in both cases recomputing the affected subtree paths.
func (m *Mock) UpdateResource(
	_ context.Context, restAPIID, resourceID string, ops []driver.PatchOperation,
) (*driver.Resource, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", resourceID)
	}

	for _, op := range ops {
		switch op.Path {
		case "/pathPart":
			res.PathPart = op.Value
		case "/parentId":
			if _, ok := ad.resources[op.Value]; !ok {
				return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", op.Value)
			}

			res.ParentID = op.Value
		}
	}

	recomputePaths(ad.resources, resourceID)

	out := copyResource(res)

	return &out, nil
}

// recomputePaths rebuilds the Path of resourceID and every descendant from the
// current parent chain, after a rename or move.
func recomputePaths(resources map[string]*driver.Resource, resourceID string) {
	for _, id := range descendants(resources, resourceID) {
		r := resources[id]
		if r.ParentID == "" {
			continue
		}

		if parent, ok := resources[r.ParentID]; ok {
			r.Path = joinPath(parent.Path, r.PathPart)
		}
	}
}

// UpdateMethod applies a patch document to a method.
func (m *Mock) UpdateMethod(
	_ context.Context, restAPIID, resourceID, httpMethod string, ops []driver.PatchOperation,
) (*driver.Method, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", resourceID)
	}

	mth, ok := res.Methods[normalizeMethod(httpMethod)]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid method identifier specified %s", httpMethod)
	}

	for _, op := range ops {
		switch op.Path {
		case "/authorizationType":
			mth.AuthorizationType = op.Value
		case "/apiKeyRequired":
			mth.APIKeyRequired = parseBool(op.Value)
		}
	}

	out := copyMethod(mth)

	return &out, nil
}

// UpdateIntegration applies a patch document to a method's integration.
func (m *Mock) UpdateIntegration(
	_ context.Context, restAPIID, resourceID, httpMethod string, ops []driver.PatchOperation,
) (*driver.Integration, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	res, ok := ad.resources[resourceID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified %s", resourceID)
	}

	mth, ok := res.Methods[normalizeMethod(httpMethod)]
	if !ok || mth.Integration == nil {
		return nil, cerrors.New(cerrors.NotFound, "No integration defined for method")
	}

	for _, op := range ops {
		applyIntegrationPatch(mth.Integration, op)
	}

	out := *mth.Integration

	return &out, nil
}

// applyIntegrationPatch applies one patch op to an Integration.
func applyIntegrationPatch(ig *driver.Integration, op driver.PatchOperation) {
	switch op.Path {
	case "/uri":
		ig.URI = op.Value
	case "/type":
		ig.Type = op.Value
	case "/integrationHttpMethod":
		ig.IntegrationHTTPMethod = op.Value
	case "/passthroughBehavior":
		ig.PassthroughBehavior = op.Value
	case "/timeoutInMillis":
		if n, err := strconv.Atoi(op.Value); err == nil {
			ig.TimeoutInMillis = n
		}
	}
}

// UpdateDeployment applies a patch document to a deployment (only /description
// is mutable).
func (m *Mock) UpdateDeployment(
	_ context.Context, restAPIID, deploymentID string, ops []driver.PatchOperation,
) (*driver.Deployment, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	dep, ok := ad.deployments[deploymentID]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", deploymentID)
	}

	for _, op := range ops {
		if op.Path == pathDescription {
			dep.Description = op.Value
		}
	}

	out := *dep

	return &out, nil
}

// UpdateStage applies a patch document to a stage.
func (m *Mock) UpdateStage(
	_ context.Context, restAPIID, stageName string, ops []driver.PatchOperation,
) (*driver.Stage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	st, ok := ad.stages[stageName]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stageName)
	}

	for _, op := range ops {
		if err := applyStagePatch(ad, st, op); err != nil {
			return nil, err
		}
	}

	out := copyStage(st)

	return &out, nil
}

// applyStagePatch applies one patch op to a Stage, validating a /deploymentId
// re-point against the API's deployments.
func applyStagePatch(ad *apiData, st *driver.Stage, op driver.PatchOperation) error {
	switch {
	case op.Path == pathDescription:
		st.Description = op.Value
	case op.Path == "/deploymentId":
		if _, ok := ad.deployments[op.Value]; !ok {
			return cerrors.Newf(cerrors.NotFound, "Invalid deployment identifier specified %s", op.Value)
		}

		st.DeploymentID = op.Value
	case strings.HasPrefix(op.Path, "/variables/"):
		key := unescapePointer(strings.TrimPrefix(op.Path, "/variables/"))
		if op.Op == opRemove {
			delete(st.Variables, key)

			return nil
		}

		if st.Variables == nil {
			st.Variables = map[string]string{}
		}

		st.Variables[key] = op.Value
	}

	return nil
}

// patchStringSlice adds or removes v from a string slice (used for the
// add/remove-valued list paths such as binaryMediaTypes).
func patchStringSlice(s []string, op, v string) []string {
	switch op {
	case opAdd, opReplace:
		for _, e := range s {
			if e == v {
				return s
			}
		}

		return append(s, v)
	case opRemove:
		out := s[:0:0]

		for _, e := range s {
			if e != v {
				out = append(out, e)
			}
		}

		return out
	default:
		return s
	}
}

// copyMethod returns a deep copy of a method and its integration.
func copyMethod(mth *driver.Method) driver.Method {
	out := *mth

	if mth.Integration != nil {
		ig := *mth.Integration
		out.Integration = &ig
	}

	return out
}

// parseBool reports whether an on-the-wire patch value (always a string) is
// true.
func parseBool(v string) bool {
	b, _ := strconv.ParseBool(v)

	return b
}

// unescapePointer decodes a single JSON Pointer reference token ("~1" -> "/",
// "~0" -> "~"), so a map key or media type carrying those characters round-trips.
func unescapePointer(tok string) string {
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")

	return tok
}
