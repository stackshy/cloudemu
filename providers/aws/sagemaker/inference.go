package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Model ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateModel(_ context.Context, cfg driver.ModelConfig) (*driver.Model, error) {
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
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
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if cfg.ConfigName == "" {
		return nil, errors.New(errors.InvalidArgument, "endpointConfigName is required")
	}

	if err := m.validateVariants(cfg.ProductionVariants); err != nil {
		return nil, err
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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
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
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "inferenceComponentName is required")
	}

	ep, err := m.DescribeEndpoint(context.Background(), cfg.EndpointName)
	if err != nil {
		return nil, errors.Newf(errors.InvalidArgument, "endpoint %q not found", cfg.EndpointName)
	}

	if cfg.ModelName != "" && !m.models.Has(cfg.ModelName) {
		return nil, errors.Newf(errors.InvalidArgument, "model %q not found", cfg.ModelName)
	}

	if cfg.VariantName != "" && !hasVariant(ep, cfg.VariantName) {
		return nil, errors.Newf(errors.InvalidArgument, "variant %q is not a production variant of endpoint %q", cfg.VariantName, cfg.EndpointName) //nolint:lll // SDK-shaped literal
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

//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListInferenceComponents(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.InferenceComponent], error) {
	return listFrom(m.inferenceComponents, listSpec[driver.InferenceComponent]{
		name:     func(t *driver.InferenceComponent) string { return t.Name },
		status:   func(t *driver.InferenceComponent) string { return t.Status },
		created:  func(t *driver.InferenceComponent) string { return t.CreationTime },
		modified: func(t *driver.InferenceComponent) string { return t.LastModifiedTime },
		extra: func(t *driver.InferenceComponent, f map[string]string) bool {
			return equalsOrUnset(f["EndpointNameEquals"], t.EndpointName) && equalsOrUnset(f["VariantNameEquals"], t.VariantName)
		},
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

// Production variant limits of an endpoint configuration.
const (
	maxEndpointVariants = 10
	maxVariantInstances = 1000
)

// validateVariants checks an endpoint configuration's variants: 1-10, named
// uniquely, each with an existing model and a non-negative count and weight.
//
//nolint:gocyclo // validation branches mirror the service's rules
func (m *Mock) validateVariants(variants []driver.ProductionVariant) error {
	if len(variants) < 1 || len(variants) > maxEndpointVariants {
		return errors.Newf(errors.InvalidArgument, "productionVariants must contain between 1 and %d items", maxEndpointVariants)
	}

	seen := map[string]bool{}

	for i := range variants {
		v := &variants[i]

		switch {
		case v.VariantName == "" || seen[v.VariantName]:
			return errors.Newf(errors.InvalidArgument, "variantName %q must be set and unique", v.VariantName)
		case v.ModelName != "" && !m.models.Has(v.ModelName): // inference-component variants name no model
			return errors.Newf(errors.InvalidArgument, "Could not find model %q.", v.ModelName)
		case v.InitialInstanceCount < 0 || v.InitialInstanceCount > maxVariantInstances:
			return errors.Newf(errors.InvalidArgument, "initialInstanceCount must be between 0 and %d", maxVariantInstances)
		case v.InitialVariantWeight < 0:
			return errors.New(errors.InvalidArgument, "initialVariantWeight must not be negative")
		}

		seen[v.VariantName] = true
	}

	return nil
}

func hasVariant(ep *driver.Endpoint, name string) bool {
	for i := range ep.Variants {
		if ep.Variants[i].VariantName == name {
			return true
		}
	}

	return false
}
