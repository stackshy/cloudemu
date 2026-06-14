package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// --- Model ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateModel(_ context.Context, cfg driver.ModelConfig) (*driver.Model, error) {
	if cfg.ModelName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelName is required")
	}

	if m.models.Has(cfg.ModelName) {
		return nil, errors.Newf(errors.AlreadyExists, "model %q already exists", cfg.ModelName)
	}

	arn := m.arn("model/" + cfg.ModelName)
	model := &driver.Model{
		ModelName:    cfg.ModelName,
		ModelARN:     arn,
		RoleARN:      cfg.RoleARN,
		Containers:   copyContainers(cfg.Containers),
		Pipeline:     cfg.Pipeline,
		CreationTime: m.now(),
		Tags:         copyTags(cfg.Tags),
	}
	m.models.Set(cfg.ModelName, model)
	m.setTags(arn, cfg.Tags)
	m.emitResourceCreated("Model")

	return cloneModel(model), nil
}

// cloneModel returns a deep copy so callers never alias the stored slices.
func cloneModel(in *driver.Model) *driver.Model {
	out := *in
	out.Containers = copyContainers(in.Containers)
	out.Tags = copyTags(in.Tags)

	return &out
}

func (m *Mock) DescribeModel(_ context.Context, name string) (*driver.Model, error) {
	model, ok := m.models.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	return cloneModel(model), nil
}

func (m *Mock) ListModels(_ context.Context) ([]driver.Model, error) {
	all := m.models.All()
	out := make([]driver.Model, 0, len(all))

	for _, v := range all {
		out = append(out, *cloneModel(v))
	}

	return out, nil
}

func (m *Mock) DeleteModel(_ context.Context, name string) error {
	if !m.models.Has(name) {
		return errors.Newf(errors.NotFound, "model %q not found", name)
	}

	m.models.Delete(name)

	return nil
}

// --- Endpoint config ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateEndpointConfig(_ context.Context, cfg driver.EndpointConfigSpec) (*driver.EndpointConfig, error) {
	if cfg.ConfigName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointConfigName is required")
	}

	if m.endpointConfigs.Has(cfg.ConfigName) {
		return nil, errors.Newf(errors.AlreadyExists, "endpoint config %q already exists", cfg.ConfigName)
	}

	arn := m.arn("endpoint-config/" + cfg.ConfigName)
	ec := &driver.EndpointConfig{
		ConfigName:         cfg.ConfigName,
		ConfigARN:          arn,
		ProductionVariants: copyVariants(cfg.ProductionVariants),
		Serverless:         cfg.Serverless,
		AsyncOutputS3URI:   cfg.AsyncOutputS3URI,
		CreationTime:       m.now(),
		Tags:               copyTags(cfg.Tags),
	}
	m.endpointConfigs.Set(cfg.ConfigName, ec)
	m.setTags(arn, cfg.Tags)

	return cloneEndpointConfig(ec), nil
}

// cloneEndpointConfig deep-copies the variant slice so callers never alias the
// stored config.
func cloneEndpointConfig(in *driver.EndpointConfig) *driver.EndpointConfig {
	out := *in
	out.ProductionVariants = copyVariants(in.ProductionVariants)
	out.Tags = copyTags(in.Tags)

	return &out
}

func (m *Mock) DescribeEndpointConfig(_ context.Context, name string) (*driver.EndpointConfig, error) {
	ec, ok := m.endpointConfigs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint config %q not found", name)
	}

	return cloneEndpointConfig(ec), nil
}

func (m *Mock) ListEndpointConfigs(_ context.Context) ([]driver.EndpointConfig, error) {
	all := m.endpointConfigs.All()
	out := make([]driver.EndpointConfig, 0, len(all))

	for _, v := range all {
		out = append(out, *cloneEndpointConfig(v))
	}

	return out, nil
}

func (m *Mock) DeleteEndpointConfig(_ context.Context, name string) error {
	if !m.endpointConfigs.Has(name) {
		return errors.Newf(errors.NotFound, "endpoint config %q not found", name)
	}

	m.endpointConfigs.Delete(name)

	return nil
}

// --- Endpoint ---

func (m *Mock) CreateEndpoint(_ context.Context, cfg driver.EndpointSpec) (*driver.Endpoint, error) {
	if cfg.EndpointName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointName is required")
	}

	if cfg.ConfigName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointConfigName is required")
	}

	if m.endpoints.Has(cfg.EndpointName) {
		return nil, errors.Newf(errors.AlreadyExists, "endpoint %q already exists", cfg.EndpointName)
	}

	ec, ok := m.endpointConfigs.Get(cfg.ConfigName)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint config %q not found", cfg.ConfigName)
	}

	now := m.now()
	arn := m.arn("endpoint/" + cfg.EndpointName)
	ep := &driver.Endpoint{
		EndpointName:     cfg.EndpointName,
		EndpointARN:      arn,
		ConfigName:       cfg.ConfigName,
		Status:           driver.EndpointInService, // synchronous Creating -> InService
		Variants:         copyVariants(ec.ProductionVariants),
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.endpoints.Set(cfg.EndpointName, ep)
	m.setTags(arn, cfg.Tags)
	m.emitResourceCreated("Endpoint")

	return cloneEndpoint(ep), nil
}

// cloneEndpoint deep-copies the variant slice so callers never alias stored
// state.
func cloneEndpoint(in *driver.Endpoint) *driver.Endpoint {
	out := *in
	out.Variants = copyVariants(in.Variants)
	out.Tags = copyTags(in.Tags)

	return &out
}

func (m *Mock) DescribeEndpoint(_ context.Context, name string) (*driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	return cloneEndpoint(ep), nil
}

func (m *Mock) ListEndpoints(_ context.Context) ([]driver.Endpoint, error) {
	all := m.endpoints.All()
	out := make([]driver.Endpoint, 0, len(all))

	for _, v := range all {
		out = append(out, *cloneEndpoint(v))
	}

	return out, nil
}

func (m *Mock) UpdateEndpoint(_ context.Context, name, configName string) (*driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	ec, ok := m.endpointConfigs.Get(configName)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint config %q not found", configName)
	}

	// Copy-then-Set: never mutate the stored pointer in place, so concurrent
	// readers see either the old or the new endpoint atomically.
	updated := *ep
	updated.ConfigName = configName
	updated.Variants = copyVariants(ec.ProductionVariants)
	updated.Status = driver.EndpointInService
	updated.LastModifiedTime = m.now()
	m.endpoints.Set(name, &updated)

	return cloneEndpoint(&updated), nil
}

func (m *Mock) UpdateEndpointWeightsAndCapacities(
	_ context.Context, name string, weights []driver.VariantWeight,
) (*driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	byName := make(map[string]driver.VariantWeight, len(weights))
	for _, w := range weights {
		byName[w.VariantName] = w
	}

	// Copy-then-Set: build a fresh variant slice so neither the stored endpoint
	// nor the source EndpointConfig is mutated in place.
	updated := *ep
	updated.Variants = copyVariants(ep.Variants)

	for i := range updated.Variants {
		if w, ok := byName[updated.Variants[i].VariantName]; ok {
			updated.Variants[i].InitialVariantWeight = w.DesiredWeight
			if w.DesiredInstanceCount > 0 {
				updated.Variants[i].InitialInstanceCount = w.DesiredInstanceCount
			}
		}
	}

	updated.Status = driver.EndpointInService
	updated.LastModifiedTime = m.now()
	m.endpoints.Set(name, &updated)

	return cloneEndpoint(&updated), nil
}

func (m *Mock) DeleteEndpoint(_ context.Context, name string) error {
	if !m.endpoints.Has(name) {
		return errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	m.endpoints.Delete(name)

	return nil
}

// --- Inference component ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateInferenceComponent(_ context.Context, cfg driver.InferenceComponentSpec) (*driver.InferenceComponent, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "inferenceComponentName is required")
	}

	if m.inferenceComponents.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "inference component %q already exists", cfg.Name)
	}

	if !m.endpoints.Has(cfg.EndpointName) {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint %q not found", cfg.EndpointName)
	}

	now := m.now()
	arn := m.arn("inference-component/" + cfg.Name)
	ic := &driver.InferenceComponent{
		Name:             cfg.Name,
		ARN:              arn,
		EndpointName:     cfg.EndpointName,
		ModelName:        cfg.ModelName,
		VariantName:      cfg.VariantName,
		CopyCount:        cfg.CopyCount,
		Status:           driver.IInService,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.inferenceComponents.Set(cfg.Name, ic)
	m.setTags(arn, cfg.Tags)

	out := *ic

	return &out, nil
}

func (m *Mock) DescribeInferenceComponent(_ context.Context, name string) (*driver.InferenceComponent, error) {
	ic, ok := m.inferenceComponents.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "inference component %q not found", name)
	}

	out := *ic

	return &out, nil
}

func (m *Mock) ListInferenceComponents(_ context.Context) ([]driver.InferenceComponent, error) {
	all := m.inferenceComponents.All()
	out := make([]driver.InferenceComponent, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteInferenceComponent(_ context.Context, name string) error {
	if !m.inferenceComponents.Has(name) {
		return errors.Newf(errors.NotFound, "inference component %q not found", name)
	}

	m.inferenceComponents.Delete(name)

	return nil
}
