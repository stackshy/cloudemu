package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Model ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateModel(_ context.Context, cfg driver.ModelConfig) (*driver.Model, error) {
	if cfg.ModelName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelName is required")
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

	if !m.models.SetIfAbsent(cfg.ModelName, model) {
		return nil, errors.Newf(errors.AlreadyExists, "model %q already exists", cfg.ModelName)
	}
	m.setTags(arn, cfg.Tags)

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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListModels(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Model], error) {
	return listFrom(m.models, listSpec[driver.Model]{
		name:    func(t *driver.Model) string { return t.ModelName },
		created: func(t *driver.Model) string { return t.CreationTime },
		clone:   func(t *driver.Model) driver.Model { return *cloneModel(t) },
	}, opts)
}

func (m *Mock) DeleteModel(_ context.Context, name string) error {
	if !m.models.Has(name) {
		return errors.Newf(errors.NotFound, "model %q not found", name)
	}

	m.models.Delete(name)
	m.dropTags(m.arn("model/" + name))

	return nil
}

// --- Endpoint config ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateEndpointConfig(_ context.Context, cfg driver.EndpointConfigSpec) (*driver.EndpointConfig, error) {
	if cfg.ConfigName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointConfigName is required")
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

	if !m.endpointConfigs.SetIfAbsent(cfg.ConfigName, ec) {
		return nil, errors.Newf(errors.AlreadyExists, "endpoint config %q already exists", cfg.ConfigName)
	}
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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListEndpointConfigs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.EndpointConfig], error) {
	return listFrom(m.endpointConfigs, listSpec[driver.EndpointConfig]{
		name:    func(t *driver.EndpointConfig) string { return t.ConfigName },
		created: func(t *driver.EndpointConfig) string { return t.CreationTime },
		clone:   func(t *driver.EndpointConfig) driver.EndpointConfig { return *cloneEndpointConfig(t) },
	}, opts)
}

func (m *Mock) DeleteEndpointConfig(_ context.Context, name string) error {
	if !m.endpointConfigs.Has(name) {
		return errors.Newf(errors.NotFound, "endpoint config %q not found", name)
	}

	m.endpointConfigs.Delete(name)
	m.dropTags(m.arn("endpoint-config/" + name))

	return nil
}

// --- Inference component ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateInferenceComponent(_ context.Context, cfg driver.InferenceComponentSpec) (*driver.InferenceComponent, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "inferenceComponentName is required")
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

	if !m.inferenceComponents.SetIfAbsent(cfg.Name, ic) {
		return nil, errors.Newf(errors.AlreadyExists, "inference component %q already exists", cfg.Name)
	}
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

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListInferenceComponents(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.InferenceComponent], error) {
	return listFrom(m.inferenceComponents, listSpec[driver.InferenceComponent]{
		name:     func(t *driver.InferenceComponent) string { return t.Name },
		status:   func(t *driver.InferenceComponent) string { return t.Status },
		created:  func(t *driver.InferenceComponent) string { return t.CreationTime },
		modified: func(t *driver.InferenceComponent) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) DeleteInferenceComponent(_ context.Context, name string) error {
	if !m.inferenceComponents.Has(name) {
		return errors.Newf(errors.NotFound, "inference component %q not found", name)
	}

	m.inferenceComponents.Delete(name)
	m.dropTags(m.arn("inference-component/" + name))

	return nil
}
