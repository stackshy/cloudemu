package vertexai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) UploadModel(_ context.Context, cfg driver.ModelConfig) (*driver.Operation, *driver.Model, error) {
	now := m.now()
	name := m.resName(cfg.Location, "models", m.newID())
	model := &driver.Model{
		Name:           name,
		DisplayName:    cfg.DisplayName,
		Description:    cfg.Description,
		ContainerImage: cfg.ContainerImage,
		ArtifactURI:    cfg.ArtifactURI,
		VersionID:      "1",
		VersionAliases: []string{"default"},
		Labels:         copyLabels(cfg.Labels),
		CreateTime:     now,
		UpdateTime:     now,
	}
	m.models.Set(name, model)
	m.emitMetric("model/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	return m.doneOp(cfg.Location, name), cloneModel(model), nil
}

func (m *Mock) GetModel(_ context.Context, name string) (*driver.Model, error) {
	base, _, _ := strings.Cut(name, "@")

	model, ok := m.models.Get(base)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	return cloneModel(model), nil
}

func (m *Mock) ListModels(_ context.Context, location string) ([]driver.Model, error) {
	out := make([]driver.Model, 0)

	for _, model := range m.models.All() {
		if location == "" || locationOf(model.Name) == location {
			out = append(out, *cloneModel(model))
		}
	}

	return out, nil
}

func (m *Mock) PatchModel(_ context.Context, name, displayName, description string) (*driver.Model, error) {
	model, ok := m.models.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	// Copy-then-Set: never mutate the stored pointer in place.
	updated := *model
	if displayName != "" {
		updated.DisplayName = displayName
	}

	if description != "" {
		updated.Description = description
	}

	updated.UpdateTime = m.now()
	m.models.Set(name, &updated)

	return cloneModel(&updated), nil
}

func (m *Mock) DeleteModel(_ context.Context, name string) (*driver.Operation, error) {
	if !m.models.Has(name) {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	m.models.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) ListModelVersions(_ context.Context, name string) ([]driver.Model, error) {
	base, _, _ := strings.Cut(name, "@")

	model, ok := m.models.Get(base)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	return []driver.Model{*cloneModel(model)}, nil
}

func (m *Mock) DeleteModelVersion(_ context.Context, name string) (*driver.Operation, error) {
	base, _, _ := strings.Cut(name, "@")

	if !m.models.Has(base) {
		return nil, errors.Newf(errors.NotFound, "model version %q not found", name)
	}

	m.models.Delete(base)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) ImportModelEvaluation(_ context.Context, modelName string, eval driver.ModelEvaluation) (*driver.ModelEvaluation, error) {
	if !m.models.Has(modelName) {
		return nil, errors.Newf(errors.NotFound, "model %q not found", modelName)
	}

	name := modelName + "/evaluations/" + m.newID()
	ev := &driver.ModelEvaluation{
		Name:        name,
		DisplayName: eval.DisplayName,
		MetricsType: eval.MetricsType,
		CreateTime:  m.now(),
	}
	m.evaluations.Set(name, ev)
	out := *ev

	return &out, nil
}

func (m *Mock) GetModelEvaluation(_ context.Context, name string) (*driver.ModelEvaluation, error) {
	ev, ok := m.evaluations.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model evaluation %q not found", name)
	}

	out := *ev

	return &out, nil
}

func (m *Mock) ListModelEvaluations(_ context.Context, modelName string) ([]driver.ModelEvaluation, error) {
	out := make([]driver.ModelEvaluation, 0)

	for _, ev := range m.evaluations.All() {
		if strings.HasPrefix(ev.Name, modelName+"/evaluations/") {
			out = append(out, *ev)
		}
	}

	return out, nil
}
