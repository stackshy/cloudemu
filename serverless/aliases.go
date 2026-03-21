package serverless

import (
	"context"

	"github.com/stackshy/cloudemu/serverless/driver"
)

// CreateAlias creates a new alias pointing to a specific function version.
func (s *Serverless) CreateAlias(ctx context.Context, config driver.AliasConfig) (*driver.Alias, error) {
	out, err := s.do(ctx, "CreateAlias", config, func() (any, error) {
		return s.driver.CreateAlias(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Alias), nil
}

// UpdateAlias updates an existing alias configuration.
func (s *Serverless) UpdateAlias(ctx context.Context, config driver.AliasConfig) (*driver.Alias, error) {
	out, err := s.do(ctx, "UpdateAlias", config, func() (any, error) {
		return s.driver.UpdateAlias(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Alias), nil
}

// DeleteAlias removes an alias from a function.
func (s *Serverless) DeleteAlias(ctx context.Context, functionName, aliasName string) error {
	_, err := s.do(ctx, "DeleteAlias", functionName, func() (any, error) {
		return nil, s.driver.DeleteAlias(ctx, functionName, aliasName)
	})

	return err
}

// GetAlias retrieves a specific alias for a function.
func (s *Serverless) GetAlias(ctx context.Context, functionName, aliasName string) (*driver.Alias, error) {
	out, err := s.do(ctx, "GetAlias", functionName, func() (any, error) {
		return s.driver.GetAlias(ctx, functionName, aliasName)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Alias), nil
}

// ListAliases returns all aliases for a function.
func (s *Serverless) ListAliases(ctx context.Context, functionName string) ([]driver.Alias, error) {
	out, err := s.do(ctx, "ListAliases", functionName, func() (any, error) {
		return s.driver.ListAliases(ctx, functionName)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Alias), nil
}
