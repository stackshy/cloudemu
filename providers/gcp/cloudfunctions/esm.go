package cloudfunctions

import (
	"context"
	"fmt"
	"sync/atomic"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// mappingCounter is used to generate unique UUIDs for event source mappings.
//
//nolint:gochecknoglobals // atomic counter needed for unique mapping UUID generation across instances.
var mappingCounter uint64

// CreateEventSourceMapping creates a new event source mapping.
func (m *Mock) CreateEventSourceMapping(
	_ context.Context, cfg driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	if cfg.FunctionName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "function name is required")
	}

	if cfg.EventSourceArn == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "event source ARN is required")
	}

	batchSize := cfg.BatchSize
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}

	state := stateDisabled
	if cfg.Enabled {
		state = stateEnabled
	}

	seq := atomic.AddUint64(&mappingCounter, 1)
	uuid := fmt.Sprintf("esm-%d", seq)

	info := &driver.EventSourceMappingInfo{
		UUID:             uuid,
		EventSourceArn:   cfg.EventSourceArn,
		FunctionName:     cfg.FunctionName,
		BatchSize:        batchSize,
		Enabled:          cfg.Enabled,
		StartingPosition: cfg.StartingPosition,
		State:            state,
		CreatedAt:        m.opts.Clock.Now().UTC().Format(timeFormat),
	}

	m.mappings.Set(uuid, info)

	result := *info

	return &result, nil
}

// DeleteEventSourceMapping deletes an event source mapping by UUID.
func (m *Mock) DeleteEventSourceMapping(_ context.Context, uuid string) error {
	if !m.mappings.Has(uuid) {
		return cerrors.Newf(cerrors.NotFound, "event source mapping %s not found", uuid)
	}

	m.mappings.Delete(uuid)

	return nil
}

// GetEventSourceMapping retrieves an event source mapping by UUID.
func (m *Mock) GetEventSourceMapping(_ context.Context, uuid string) (*driver.EventSourceMappingInfo, error) {
	info, ok := m.mappings.Get(uuid)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "event source mapping %s not found", uuid)
	}

	result := *info

	return &result, nil
}

// ListEventSourceMappings lists event source mappings, optionally filtered by function name.
func (m *Mock) ListEventSourceMappings(_ context.Context, functionName string) ([]driver.EventSourceMappingInfo, error) {
	all := m.mappings.All()
	result := make([]driver.EventSourceMappingInfo, 0, len(all))

	for _, info := range all {
		if functionName == "" || info.FunctionName == functionName {
			result = append(result, *info)
		}
	}

	return result, nil
}

// UpdateEventSourceMapping updates an existing event source mapping.
func (m *Mock) UpdateEventSourceMapping(
	_ context.Context, uuid string, cfg driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	info, ok := m.mappings.Get(uuid)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "event source mapping %s not found", uuid)
	}

	if cfg.FunctionName != "" {
		info.FunctionName = cfg.FunctionName
	}

	if cfg.EventSourceArn != "" {
		info.EventSourceArn = cfg.EventSourceArn
	}

	if cfg.BatchSize != 0 {
		info.BatchSize = cfg.BatchSize
	}

	info.Enabled = cfg.Enabled

	if cfg.Enabled {
		info.State = stateEnabled
	} else {
		info.State = stateDisabled
	}

	m.mappings.Set(uuid, info)

	result := *info

	return &result, nil
}
