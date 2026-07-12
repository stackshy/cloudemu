package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// --- Metadata stores ---

func (v *VertexAI) CreateMetadataStore(
	ctx context.Context, location, storeID string,
) (*driver.Operation, *driver.MetadataStore, error) {
	r, err := cast[opPair[*driver.MetadataStore]](v.do(ctx, "CreateMetadataStore", location, func() (any, error) {
		op, s, e := v.drv.CreateMetadataStore(ctx, location, storeID)

		return opPair[*driver.MetadataStore]{op, s}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetMetadataStore(ctx context.Context, name string) (*driver.MetadataStore, error) {
	return cast[*driver.MetadataStore](v.do(ctx, "GetMetadataStore", name, func() (any, error) {
		return v.drv.GetMetadataStore(ctx, name)
	}))
}

func (v *VertexAI) ListMetadataStores(ctx context.Context, location string) ([]driver.MetadataStore, error) {
	return cast[[]driver.MetadataStore](v.do(ctx, "ListMetadataStores", location, func() (any, error) {
		return v.drv.ListMetadataStores(ctx, location)
	}))
}

func (v *VertexAI) DeleteMetadataStore(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteMetadataStore", name, func() (any, error) {
		return v.drv.DeleteMetadataStore(ctx, name)
	}))
}

// --- Tensorboards ---

func (v *VertexAI) CreateTensorboard(
	ctx context.Context, location, displayName string,
) (*driver.Operation, *driver.Tensorboard, error) {
	r, err := cast[opPair[*driver.Tensorboard]](v.do(ctx, "CreateTensorboard", location, func() (any, error) {
		op, tb, e := v.drv.CreateTensorboard(ctx, location, displayName)

		return opPair[*driver.Tensorboard]{op, tb}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetTensorboard(ctx context.Context, name string) (*driver.Tensorboard, error) {
	return cast[*driver.Tensorboard](v.do(ctx, "GetTensorboard", name, func() (any, error) { return v.drv.GetTensorboard(ctx, name) }))
}

func (v *VertexAI) ListTensorboards(ctx context.Context, location string) ([]driver.Tensorboard, error) {
	return cast[[]driver.Tensorboard](v.do(ctx, "ListTensorboards", location, func() (any, error) {
		return v.drv.ListTensorboards(ctx, location)
	}))
}

func (v *VertexAI) DeleteTensorboard(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteTensorboard", name, func() (any, error) {
		return v.drv.DeleteTensorboard(ctx, name)
	}))
}

// --- Schedules ---

func (v *VertexAI) CreateSchedule(ctx context.Context, location, displayName, cron string) (*driver.Schedule, error) {
	return cast[*driver.Schedule](v.do(ctx, "CreateSchedule", location, func() (any, error) {
		return v.drv.CreateSchedule(ctx, location, displayName, cron)
	}))
}

func (v *VertexAI) GetSchedule(ctx context.Context, name string) (*driver.Schedule, error) {
	return cast[*driver.Schedule](v.do(ctx, "GetSchedule", name, func() (any, error) { return v.drv.GetSchedule(ctx, name) }))
}

func (v *VertexAI) ListSchedules(ctx context.Context, location string) ([]driver.Schedule, error) {
	return cast[[]driver.Schedule](v.do(ctx, "ListSchedules", location, func() (any, error) { return v.drv.ListSchedules(ctx, location) }))
}

func (v *VertexAI) PauseSchedule(ctx context.Context, name string) error {
	_, err := v.do(ctx, "PauseSchedule", name, func() (any, error) { return nil, v.drv.PauseSchedule(ctx, name) })

	return err
}

func (v *VertexAI) ResumeSchedule(ctx context.Context, name string) error {
	_, err := v.do(ctx, "ResumeSchedule", name, func() (any, error) { return nil, v.drv.ResumeSchedule(ctx, name) })

	return err
}

func (v *VertexAI) DeleteSchedule(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteSchedule", name, func() (any, error) { return v.drv.DeleteSchedule(ctx, name) }))
}

// --- Notebook runtime templates ---

func (v *VertexAI) CreateNotebookRuntimeTemplate(
	ctx context.Context, location, displayName, machineType string,
) (*driver.Operation, *driver.NotebookRuntimeTemplate, error) {
	r, err := cast[opPair[*driver.NotebookRuntimeTemplate]](v.do(ctx, "CreateNotebookRuntimeTemplate", location, func() (any, error) {
		op, t, e := v.drv.CreateNotebookRuntimeTemplate(ctx, location, displayName, machineType)

		return opPair[*driver.NotebookRuntimeTemplate]{op, t}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetNotebookRuntimeTemplate(ctx context.Context, name string) (*driver.NotebookRuntimeTemplate, error) {
	return cast[*driver.NotebookRuntimeTemplate](v.do(ctx, "GetNotebookRuntimeTemplate", name, func() (any, error) {
		return v.drv.GetNotebookRuntimeTemplate(ctx, name)
	}))
}

func (v *VertexAI) ListNotebookRuntimeTemplates(ctx context.Context, location string) ([]driver.NotebookRuntimeTemplate, error) {
	return cast[[]driver.NotebookRuntimeTemplate](v.do(ctx, "ListNotebookRuntimeTemplates", location, func() (any, error) {
		return v.drv.ListNotebookRuntimeTemplates(ctx, location)
	}))
}

func (v *VertexAI) DeleteNotebookRuntimeTemplate(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteNotebookRuntimeTemplate", name, func() (any, error) {
		return v.drv.DeleteNotebookRuntimeTemplate(ctx, name)
	}))
}

// --- Notebook runtimes ---

func (v *VertexAI) AssignNotebookRuntime(
	ctx context.Context, location, displayName string,
) (*driver.Operation, *driver.NotebookRuntime, error) {
	r, err := cast[opPair[*driver.NotebookRuntime]](v.do(ctx, "AssignNotebookRuntime", location, func() (any, error) {
		op, nr, e := v.drv.AssignNotebookRuntime(ctx, location, displayName)

		return opPair[*driver.NotebookRuntime]{op, nr}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetNotebookRuntime(ctx context.Context, name string) (*driver.NotebookRuntime, error) {
	return cast[*driver.NotebookRuntime](v.do(ctx, "GetNotebookRuntime", name, func() (any, error) {
		return v.drv.GetNotebookRuntime(ctx, name)
	}))
}

func (v *VertexAI) ListNotebookRuntimes(ctx context.Context, location string) ([]driver.NotebookRuntime, error) {
	return cast[[]driver.NotebookRuntime](v.do(ctx, "ListNotebookRuntimes", location, func() (any, error) {
		return v.drv.ListNotebookRuntimes(ctx, location)
	}))
}

func (v *VertexAI) StartNotebookRuntime(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "StartNotebookRuntime", name, func() (any, error) {
		return v.drv.StartNotebookRuntime(ctx, name)
	}))
}

func (v *VertexAI) StopNotebookRuntime(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "StopNotebookRuntime", name, func() (any, error) {
		return v.drv.StopNotebookRuntime(ctx, name)
	}))
}

func (v *VertexAI) DeleteNotebookRuntime(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteNotebookRuntime", name, func() (any, error) {
		return v.drv.DeleteNotebookRuntime(ctx, name)
	}))
}

// --- Operations ---

func (v *VertexAI) GetOperation(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "GetOperation", name, func() (any, error) { return v.drv.GetOperation(ctx, name) }))
}

func (v *VertexAI) ListOperations(ctx context.Context, parent string) ([]driver.Operation, error) {
	return cast[[]driver.Operation](v.do(ctx, "ListOperations", parent, func() (any, error) { return v.drv.ListOperations(ctx, parent) }))
}
