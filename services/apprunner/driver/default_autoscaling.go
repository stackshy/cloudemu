package driver

import "context"

// DefaultAutoScaling is the optional capability to move the account's default auto
// scaling configuration and to see which services use a configuration.
type DefaultAutoScaling interface {
	UpdateDefaultAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	ListServicesForAutoScalingConfiguration(ctx context.Context, arn string, page Page) (
		serviceArns []string, nextToken string, err error)
}
