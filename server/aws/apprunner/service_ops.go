package apprunner

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// registerServiceRoutes wires the service and operation-history operations.
func (h *Handler) registerServiceRoutes() {
	h.routes["CreateService"] = h.createService
	h.routes["DescribeService"] = h.describeService
	h.routes["UpdateService"] = h.updateService
	h.routes["DeleteService"] = h.deleteService
	h.routes["ListServices"] = h.listServices
	h.routes["PauseService"] = h.pauseService
	h.routes["ResumeService"] = h.resumeService
	h.routes["StartDeployment"] = h.startDeployment
	h.routes["ListOperations"] = h.listOperations
}

type createServiceRequest struct {
	ServiceName                 string                                 `json:"ServiceName"`
	SourceConfiguration         *sourceConfigurationJSON               `json:"SourceConfiguration"`
	InstanceConfiguration       *instanceConfigurationJSON             `json:"InstanceConfiguration"`
	HealthCheckConfiguration    *healthCheckConfigurationJSON          `json:"HealthCheckConfiguration"`
	NetworkConfiguration        *networkConfigurationJSON              `json:"NetworkConfiguration"`
	ObservabilityConfiguration  *serviceObservabilityConfigurationJSON `json:"ObservabilityConfiguration"`
	EncryptionConfiguration     *encryptionConfigurationJSON           `json:"EncryptionConfiguration"`
	AutoScalingConfigurationArn string                                 `json:"AutoScalingConfigurationArn"`
	Tags                        []tagJSON                              `json:"Tags"`
}

// serviceResultResponse is the wire shape of the {OperationId, Service}
// responses shared by the mutating service operations.
type serviceResultResponse struct {
	OperationID string      `json:"OperationId,omitempty"`
	Service     serviceJSON `json:"Service"`
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createServiceRequest) (any, error) {
		res, err := h.apprunner.CreateService(ctx, &driver.CreateServiceInput{
			ServiceName:                 req.ServiceName,
			SourceConfiguration:         sourceConfigFromWire(req.SourceConfiguration),
			InstanceConfiguration:       instanceConfigFromWire(req.InstanceConfiguration),
			HealthCheckConfiguration:    healthCheckFromWire(req.HealthCheckConfiguration),
			NetworkConfiguration:        networkConfigFromWire(req.NetworkConfiguration),
			ObservabilityConfiguration:  serviceObsFromWire(req.ObservabilityConfiguration),
			EncryptionConfiguration:     encryptionFromWire(req.EncryptionConfiguration),
			AutoScalingConfigurationArn: req.AutoScalingConfigurationArn,
			Tags:                        tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return serviceResultToWire(res), nil
	})
}

type describeServiceRequest struct {
	ServiceArn string `json:"ServiceArn"`
}

type describeServiceResponse struct {
	Service serviceJSON `json:"Service"`
}

func (h *Handler) describeService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeServiceRequest) (any, error) {
		svc, err := h.apprunner.DescribeService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return describeServiceResponse{Service: serviceToWire(svc)}, nil
	})
}

type updateServiceRequest struct {
	ServiceArn                  string                                 `json:"ServiceArn"`
	SourceConfiguration         *sourceConfigurationJSON               `json:"SourceConfiguration"`
	InstanceConfiguration       *instanceConfigurationJSON             `json:"InstanceConfiguration"`
	HealthCheckConfiguration    *healthCheckConfigurationJSON          `json:"HealthCheckConfiguration"`
	NetworkConfiguration        *networkConfigurationJSON              `json:"NetworkConfiguration"`
	ObservabilityConfiguration  *serviceObservabilityConfigurationJSON `json:"ObservabilityConfiguration"`
	AutoScalingConfigurationArn string                                 `json:"AutoScalingConfigurationArn"`
}

func (h *Handler) updateService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateServiceRequest) (any, error) {
		res, err := h.apprunner.UpdateService(ctx, &driver.UpdateServiceInput{
			ServiceArn:                  req.ServiceArn,
			SourceConfiguration:         sourceConfigFromWire(req.SourceConfiguration),
			InstanceConfiguration:       instanceConfigFromWire(req.InstanceConfiguration),
			HealthCheckConfiguration:    healthCheckFromWire(req.HealthCheckConfiguration),
			NetworkConfiguration:        networkConfigFromWire(req.NetworkConfiguration),
			ObservabilityConfiguration:  serviceObsFromWire(req.ObservabilityConfiguration),
			AutoScalingConfigurationArn: req.AutoScalingConfigurationArn,
		})
		if err != nil {
			return nil, err
		}

		return serviceResultToWire(res), nil
	})
}

type deleteServiceRequest struct {
	ServiceArn string `json:"ServiceArn"`
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteServiceRequest) (any, error) {
		res, err := h.apprunner.DeleteService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return serviceResultToWire(res), nil
	})
}

// serviceSummaryJSON is the wire shape of a ServiceSummary in ListServices.
type serviceSummaryJSON struct {
	ServiceName string `json:"ServiceName"`
	ServiceID   string `json:"ServiceId"`
	ServiceArn  string `json:"ServiceArn"`
	ServiceURL  string `json:"ServiceUrl,omitempty"`
	CreatedAt   any    `json:"CreatedAt,omitempty"`
	UpdatedAt   any    `json:"UpdatedAt,omitempty"`
	Status      string `json:"Status"`
}

type listServicesRequest struct {
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listServicesResponse struct {
	ServiceSummaryList []serviceSummaryJSON `json:"ServiceSummaryList"`
	NextToken          string               `json:"NextToken,omitempty"`
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listServicesRequest) (any, error) {
		services, next, err := h.apprunner.ListServices(ctx, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		return listServicesResponse{
			ServiceSummaryList: mapWire(services, serviceSummaryToWire),
			NextToken:          next,
		}, nil
	})
}

func (h *Handler) pauseService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeServiceRequest) (any, error) {
		res, err := h.apprunner.PauseService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return serviceResultToWire(res), nil
	})
}

func (h *Handler) resumeService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeServiceRequest) (any, error) {
		res, err := h.apprunner.ResumeService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return serviceResultToWire(res), nil
	})
}

type startDeploymentResponse struct {
	OperationID string `json:"OperationId"`
}

func (h *Handler) startDeployment(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeServiceRequest) (any, error) {
		opID, err := h.apprunner.StartDeployment(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return startDeploymentResponse{OperationID: opID}, nil
	})
}

type listOperationsRequest struct {
	ServiceArn string `json:"ServiceArn"`
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listOperationsResponse struct {
	OperationSummaryList []operationSummaryJSON `json:"OperationSummaryList"`
	NextToken            string                 `json:"NextToken,omitempty"`
}

func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listOperationsRequest) (any, error) {
		ops, next, err := h.apprunner.ListOperations(ctx, req.ServiceArn, pageFromWire(req.MaxResults, req.NextToken))
		if err != nil {
			return nil, err
		}

		resp := listOperationsResponse{
			OperationSummaryList: make([]operationSummaryJSON, 0, len(ops)),
			NextToken:            next,
		}
		for i := range ops {
			resp.OperationSummaryList = append(resp.OperationSummaryList, operationToWire(&ops[i]))
		}

		return resp, nil
	})
}

// serviceResultToWire renders a driver.ServiceResult as its {OperationId,
// Service} wire response.
func serviceResultToWire(res *driver.ServiceResult) serviceResultResponse {
	return serviceResultResponse{OperationID: res.OperationID, Service: serviceToWire(res.Service)}
}

// serviceSummaryToWire renders a driver.Service as its ListServices summary.
func serviceSummaryToWire(s *driver.Service) serviceSummaryJSON {
	return serviceSummaryJSON{
		ServiceName: s.ServiceName,
		ServiceID:   s.ServiceID,
		ServiceArn:  s.ServiceArn,
		ServiceURL:  s.ServiceURL,
		CreatedAt:   epochSeconds(s.CreatedAt),
		UpdatedAt:   epochSeconds(s.UpdatedAt),
		Status:      s.Status,
	}
}
