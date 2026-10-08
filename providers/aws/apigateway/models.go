package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

var (
	_ driver.Models            = (*Mock)(nil)
	_ driver.RequestValidators = (*Mock)(nil)
)

const (
	msgModelNotFound     = "Invalid Model Name specified"
	msgModelExists       = "Model name already exists in this RestApi"
	msgModelName         = "Model name must be alphanumeric"
	msgModelSchema       = "Invalid model schema specified: the schema must be a JSON object"
	msgModelReserved     = "The Empty and Error models are defined by API Gateway and cannot be changed or deleted"
	msgValidatorNotFound = "Invalid Request Validator identifier specified"
	msgValidatorName     = "Request validator name is required"
	defaultContentType   = "application/json"
	modelNameEmpty       = "Empty"
	modelNameError       = "Error"
)

var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// defaultModels returns the Empty and Error models every REST API starts with.
func defaultModels() map[string]*driver.Model {
	const schema = `{"$schema":"http://json-schema.org/draft-04/schema#","title":"%s Schema","type":"object"%s}`

	return map[string]*driver.Model{
		modelNameEmpty: {
			ID: genShortID(), Name: modelNameEmpty, Description: "This is a default empty schema model",
			ContentType: defaultContentType, Schema: fmt.Sprintf(schema, "Empty", ""),
		},
		modelNameError: {
			ID: genShortID(), Name: modelNameError, Description: "This is a default error schema model",
			ContentType: defaultContentType,
			Schema:      fmt.Sprintf(schema, "Error", `,"properties":{"message":{"type":"string"}}`),
		},
	}
}

// CreateModel adds a model. The schema must be a JSON object.
func (m *Mock) CreateModel(_ context.Context, restAPIID string, in *driver.CreateModelInput) (*driver.Model, error) {
	if !modelNamePattern.MatchString(in.Name) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgModelName)
	}

	if err := validateSchemaDoc(in.Schema); err != nil {
		return nil, err
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, exists := ad.models[in.Name]; exists {
		return nil, cerrors.New(cerrors.AlreadyExists, msgModelExists)
	}

	mod := &driver.Model{
		ID: genShortID(), Name: in.Name, Description: in.Description, Schema: in.Schema,
		ContentType: orDefault(in.ContentType, defaultContentType),
	}
	ad.models[in.Name] = mod
	out := *mod

	return &out, nil
}

// validateSchemaDoc requires a model schema to be a JSON object (or empty).
func validateSchemaDoc(schema string) error {
	if schema == "" {
		return nil
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(schema), &doc); err != nil {
		return cerrors.New(cerrors.InvalidArgument, msgModelSchema)
	}

	return nil
}

// GetModel returns one model by name.
func (m *Mock) GetModel(_ context.Context, restAPIID, name string) (*driver.Model, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	mod, ok := ad.models[name]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgModelNotFound)
	}

	out := *mod

	return &out, nil
}

// GetModels lists a REST API's models ordered by name.
func (m *Mock) GetModels(_ context.Context, restAPIID string, page driver.PageInput) (*driver.ModelPage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()

	all := make([]driver.Model, 0, len(ad.models))
	for _, mod := range ad.models {
		all = append(all, *mod)
	}

	ad.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.ModelPage{Items: items, Position: next}, nil
}

// UpdateModel applies /description and /schema patches.
func (m *Mock) UpdateModel(_ context.Context, restAPIID, name string, ops []driver.PatchOperation) (*driver.Model, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	mod, ok := ad.models[name]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgModelNotFound)
	}

	if name == modelNameEmpty || name == modelNameError {
		return nil, cerrors.New(cerrors.InvalidArgument, msgModelReserved)
	}

	upd := *mod

	for _, op := range ops {
		switch op.Path {
		case pathDescription:
			upd.Description = op.Value
		case "/schema":
			if err := validateSchemaDoc(op.Value); err != nil {
				return nil, err
			}

			upd.Schema = op.Value
		default:
			return nil, invalidPatchPath(op, pathDescription, "/schema")
		}
	}

	*mod = upd
	out := *mod

	return &out, nil
}

// DeleteModel removes a model unless a method still references it.
func (m *Mock) DeleteModel(_ context.Context, restAPIID, name string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.models[name]; !ok {
		return cerrors.New(cerrors.NotFound, msgModelNotFound)
	}

	if name == modelNameEmpty || name == modelNameError {
		return cerrors.New(cerrors.InvalidArgument, msgModelReserved)
	}

	if methodUsing(ad, func(mth *driver.Method) bool { return modelReferenced(mth, name) }) {
		return cerrors.New(cerrors.InvalidArgument, "Model is still referenced by a method; remove the reference first")
	}

	delete(ad.models, name)

	return nil
}

// CreateRequestValidator adds a request validator.
func (m *Mock) CreateRequestValidator(
	_ context.Context, restAPIID string, in *driver.CreateRequestValidatorInput,
) (*driver.RequestValidator, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgValidatorName)
	}

	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	v := &driver.RequestValidator{
		ID: genShortID(), Name: in.Name, ValidateRequestBody: in.ValidateRequestBody,
		ValidateRequestParameters: in.ValidateRequestParameters,
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ad.validators[v.ID] = v
	out := *v

	return &out, nil
}

// GetRequestValidator returns one validator.
func (m *Mock) GetRequestValidator(_ context.Context, restAPIID, id string) (*driver.RequestValidator, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	v, ok := ad.validators[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgValidatorNotFound)
	}

	out := *v

	return &out, nil
}

// GetRequestValidators lists validators ordered by name then id.
func (m *Mock) GetRequestValidators(
	_ context.Context, restAPIID string, page driver.PageInput,
) (*driver.RequestValidatorPage, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()

	all := make([]driver.RequestValidator, 0, len(ad.validators))
	for _, v := range ad.validators {
		all = append(all, *v)
	}

	ad.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.RequestValidatorPage{Items: items, Position: next}, nil
}

// UpdateRequestValidator applies /name, /validateRequestBody and
// /validateRequestParameters patches.
func (m *Mock) UpdateRequestValidator(
	_ context.Context, restAPIID, id string, ops []driver.PatchOperation,
) (*driver.RequestValidator, error) {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	v, ok := ad.validators[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgValidatorNotFound)
	}

	upd := *v

	for _, op := range ops {
		switch op.Path {
		case pathName:
			upd.Name = op.Value
		case "/validateRequestBody":
			upd.ValidateRequestBody = parseBool(op.Value)
		case "/validateRequestParameters":
			upd.ValidateRequestParameters = parseBool(op.Value)
		default:
			return nil, invalidPatchPath(op, pathName, "/validateRequestBody", "/validateRequestParameters")
		}
	}

	if upd.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgValidatorName)
	}

	*v = upd
	out := *v

	return &out, nil
}

// DeleteRequestValidator removes a validator. Methods that name it lose it.
func (m *Mock) DeleteRequestValidator(_ context.Context, restAPIID, id string) error {
	ad, err := m.getAPI(restAPIID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.validators[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgValidatorNotFound)
	}

	delete(ad.validators, id)

	for _, res := range ad.resources {
		for _, mth := range res.Methods {
			if mth.RequestValidatorID == id {
				mth.RequestValidatorID = ""
			}
		}
	}

	return nil
}

// modelReferenced reports whether a method names the model in its request models
// or in any of its method responses.
func modelReferenced(mth *driver.Method, name string) bool {
	for _, v := range mth.RequestModels {
		if v == name {
			return true
		}
	}

	for _, mr := range mth.MethodResponses {
		for _, v := range mr.ResponseModels {
			if v == name {
				return true
			}
		}
	}

	return false
}
