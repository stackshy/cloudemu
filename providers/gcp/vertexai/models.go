package vertexai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/vertexai/driver"
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
		Labels:         cfg.Labels,
		CreateTime:     now,
		UpdateTime:     now,
	}
	m.models.Set(name, model)
	m.emitMetric("model/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	out := *model

	return m.doneOp(cfg.Location, name), &out, nil
}

func (m *Mock) GetModel(_ context.Context, name string) (*driver.Model, error) {
	base, _, _ := strings.Cut(name, "@")

	model, ok := m.models.Get(base)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	out := *model

	return &out, nil
}

func (m *Mock) ListModels(_ context.Context, location string) ([]driver.Model, error) {
	out := make([]driver.Model, 0)

	for _, model := range m.models.All() {
		if location == "" || locationOf(model.Name) == location {
			out = append(out, *model)
		}
	}

	return out, nil
}

func (m *Mock) PatchModel(_ context.Context, name, displayName, description string) (*driver.Model, error) {
	model, ok := m.models.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model %q not found", name)
	}

	if displayName != "" {
		model.DisplayName = displayName
	}

	if description != "" {
		model.Description = description
	}

	model.UpdateTime = m.now()
	out := *model

	return &out, nil
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

	return []driver.Model{*model}, nil
}

func (m *Mock) DeleteModelVersion(_ context.Context, name string) (*driver.Operation, error) {
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
