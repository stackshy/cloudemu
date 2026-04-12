package serverless

import (
	"context"

	"github.com/stackshy/cloudemu/serverless/driver"
)

// CreateEventSourceMapping creates a new event source mapping.
func (s *Serverless) CreateEventSourceMapping(
	ctx context.Context, config driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	out, err := s.do(ctx, "CreateEventSourceMapping", config, func() (any, error) {
		return s.driver.CreateEventSourceMapping(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EventSourceMappingInfo), nil
}

// DeleteEventSourceMapping deletes an event source mapping by UUID.
func (s *Serverless) DeleteEventSourceMapping(ctx context.Context, uuid string) error {
	_, err := s.do(ctx, "DeleteEventSourceMapping", uuid, func() (any, error) {
		return nil, s.driver.DeleteEventSourceMapping(ctx, uuid)
	})

	return err
}

// GetEventSourceMapping retrieves an event source mapping by UUID.
func (s *Serverless) GetEventSourceMapping(
	ctx context.Context, uuid string,
) (*driver.EventSourceMappingInfo, error) {
	out, err := s.do(ctx, "GetEventSourceMapping", uuid, func() (any, error) {
		return s.driver.GetEventSourceMapping(ctx, uuid)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EventSourceMappingInfo), nil
}

// ListEventSourceMappings lists event source mappings, optionally filtered by function name.
func (s *Serverless) ListEventSourceMappings(
	ctx context.Context, functionName string,
) ([]driver.EventSourceMappingInfo, error) {
	out, err := s.do(ctx, "ListEventSourceMappings", functionName, func() (any, error) {
		return s.driver.ListEventSourceMappings(ctx, functionName)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.EventSourceMappingInfo), nil
}

// UpdateEventSourceMapping updates an existing event source mapping.
func (s *Serverless) UpdateEventSourceMapping(
	ctx context.Context, uuid string, config driver.EventSourceMappingConfig,
) (*driver.EventSourceMappingInfo, error) {
	out, err := s.do(ctx, "UpdateEventSourceMapping", config, func() (any, error) {
		return s.driver.UpdateEventSourceMapping(ctx, uuid, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EventSourceMappingInfo), nil
}
