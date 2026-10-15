package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerDefaultAutoScalingRoutes wires UpdateDefaultAutoScalingConfiguration
// and ListServicesForAutoScalingConfiguration.
func (h *Handler) registerDefaultAutoScalingRoutes(d driver.DefaultAutoScaling) {
	h.routes["UpdateDefaultAutoScalingConfiguration"] = handle(h,
		func(ctx context.Context, req *autoScalingArnRequest) (autoScalingConfigurationResponse, error) {
			cfg, err := d.UpdateDefaultAutoScalingConfiguration(ctx, req.AutoScalingConfigurationArn)
			if err != nil {
				return autoScalingConfigurationResponse{}, err
			}

			return autoScalingConfigurationResponse{AutoScalingConfiguration: autoScalingToWire(cfg)}, nil
		})
	h.routes["ListServicesForAutoScalingConfiguration"] = handle(h,
		func(ctx context.Context, req *listServicesForAutoScalingRequest) (listServicesForAutoScalingResponse, error) {
			arns, next, err := d.ListServicesForAutoScalingConfiguration(
				ctx, req.AutoScalingConfigurationArn, pageFromWire(req.MaxResults, req.NextToken))
			if err != nil {
				return listServicesForAutoScalingResponse{}, err
			}

			return listServicesForAutoScalingResponse{ServiceArnList: append([]string{}, arns...), NextToken: next}, nil
		})
}

type listServicesForAutoScalingRequest struct {
	AutoScalingConfigurationArn string `json:"AutoScalingConfigurationArn"`
	MaxResults                  int32  `json:"MaxResults"`
	NextToken                   string `json:"NextToken"`
}

type listServicesForAutoScalingResponse struct {
	ServiceArnList []string `json:"ServiceArnList"`
	NextToken      string   `json:"NextToken,omitempty"`
}
