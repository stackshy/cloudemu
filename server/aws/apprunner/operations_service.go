package apprunner

import (
	"context"
	"net/http"

	ardriver "github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func (h *Handler) createService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createServiceRequest) (any, error) {
		res, err := h.ar.CreateService(ctx, ardriver.CreateServiceInput{
			ServiceName:                req.ServiceName,
			SourceConfiguration:        req.SourceConfiguration,
			InstanceConfiguration:      req.InstanceConfiguration,
			NetworkConfiguration:       req.NetworkConfiguration,
			HealthCheckConfiguration:   req.HealthCheckConfiguration,
			EncryptionConfiguration:    req.EncryptionConfiguration,
			ObservabilityConfiguration: req.ObservabilityConfiguration,
			AutoScalingConfigArn:       req.AutoScalingConfigurationArn,
			Tags:                       tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return serviceOpResponse{Service: serviceToWire(res.Service), OperationID: res.OperationID}, nil
	})
}

func (h *Handler) describeService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *serviceArnRequest) (any, error) {
		svc, err := h.ar.DescribeService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return describeServiceResponse{Service: serviceToWire(svc)}, nil
	})
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *serviceArnRequest) (any, error) {
		res, err := h.ar.DeleteService(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return serviceOpResponse{Service: serviceToWire(res.Service), OperationID: res.OperationID}, nil
	})
}

func (h *Handler) updateService(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateServiceRequest) (any, error) {
		res, err := h.ar.UpdateService(ctx, ardriver.UpdateServiceInput{
			ServiceArn:                 req.ServiceArn,
			SourceConfiguration:        req.SourceConfiguration,
			InstanceConfiguration:      req.InstanceConfiguration,
			NetworkConfiguration:       req.NetworkConfiguration,
			HealthCheckConfiguration:   req.HealthCheckConfiguration,
			ObservabilityConfiguration: req.ObservabilityConfiguration,
			AutoScalingConfigArn:       req.AutoScalingConfigurationArn,
		})
		if err != nil {
			return nil, err
		}

		return serviceOpResponse{Service: serviceToWire(res.Service), OperationID: res.OperationID}, nil
	})
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listServicesRequest) (any, error) {
		svcs, token, err := h.ar.ListServices(ctx, req.NextToken, req.MaxResults)
		if err != nil {
			return nil, err
		}

		items := make([]serviceSummary, 0, len(svcs))

		for i := range svcs {
			s := &svcs[i]
			items = append(items, serviceSummary{
				ServiceArn: s.ServiceArn, ServiceID: s.ServiceID, ServiceName: s.ServiceName,
				ServiceURL: s.ServiceURL, Status: s.Status,
				CreatedAt: epoch(s.CreatedAt), UpdatedAt: epoch(s.UpdatedAt),
			})
		}

		return listServicesResponse{ServiceSummaryList: items, NextToken: token}, nil
	})
}

func (h *Handler) pauseService(w http.ResponseWriter, r *http.Request) {
	h.serviceTransition(w, r, h.ar.PauseService)
}

func (h *Handler) resumeService(w http.ResponseWriter, r *http.Request) {
	h.serviceTransition(w, r, h.ar.ResumeService)
}

func (h *Handler) serviceTransition(
	w http.ResponseWriter, r *http.Request,
	call func(context.Context, string) (*ardriver.ServiceResult, error),
) {
	dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *serviceArnRequest) (any, error) {
		res, err := call(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return serviceOpResponse{Service: serviceToWire(res.Service), OperationID: res.OperationID}, nil
	})
}

func (h *Handler) startDeployment(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *serviceArnRequest) (any, error) {
		opID, err := h.ar.StartDeployment(ctx, req.ServiceArn)
		if err != nil {
			return nil, err
		}

		return startDeploymentResponse{OperationID: opID}, nil
	})
}

func serviceToWire(s *ardriver.Service) wireService {
	out := wireService{
		ServiceArn: s.ServiceArn, ServiceID: s.ServiceID, ServiceName: s.ServiceName,
		ServiceURL: s.ServiceURL, Status: s.Status,
		CreatedAt: epoch(s.CreatedAt), UpdatedAt: epoch(s.UpdatedAt), DeletedAt: epoch(s.DeletedAt),
		SourceConfiguration:        s.SourceConfiguration,
		InstanceConfiguration:      s.InstanceConfiguration,
		NetworkConfiguration:       s.NetworkConfiguration,
		HealthCheckConfiguration:   s.HealthCheckConfiguration,
		EncryptionConfiguration:    s.EncryptionConfiguration,
		ObservabilityConfiguration: s.ObservabilityConfiguration,
	}
	out.AutoScalingConfigurationSummary = &ascSummary{
		AutoScalingConfigurationArn:      s.AutoScalingConfigArn,
		AutoScalingConfigurationName:     s.AutoScalingConfigName,
		AutoScalingConfigurationRevision: s.AutoScalingConfigRevision,
	}

	return out
}
