package wafv2

import (
	"context"
	"net/http"
)

func (h *Handler) checkCapacity(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *checkCapacityRequest) (any, error) {
		capacity, err := h.waf.CheckCapacity(ctx, req.Scope, req.Rules)
		if err != nil {
			return nil, err
		}

		return checkCapacityResponse{Capacity: capacity}, nil
	})
}

func (h *Handler) putLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putLoggingConfigurationRequest) (any, error) {
		cfg, err := h.waf.PutLoggingConfiguration(ctx, req.LoggingConfiguration)
		if err != nil {
			return nil, err
		}

		return putLoggingConfigurationResponse{LoggingConfiguration: cfg}, nil
	})
}

func (h *Handler) getLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getLoggingConfigurationRequest) (any, error) {
		cfg, err := h.waf.GetLoggingConfiguration(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return getLoggingConfigurationResponse{LoggingConfiguration: cfg}, nil
	})
}

func (h *Handler) deleteLoggingConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteLoggingConfigurationRequest) (any, error) {
		if err := h.waf.DeleteLoggingConfiguration(ctx, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listLoggingConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *scopeRequest) (any, error) {
		cfgs, err := h.waf.ListLoggingConfigurations(ctx, req.Scope)
		if err != nil {
			return nil, err
		}

		return listLoggingConfigurationsResponse{LoggingConfigurations: cfgs}, nil
	})
}

func (h *Handler) putPermissionPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putPermissionPolicyRequest) (any, error) {
		if err := h.waf.PutPermissionPolicy(ctx, req.ResourceArn, req.Policy); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) getPermissionPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getPermissionPolicyRequest) (any, error) {
		policy, err := h.waf.GetPermissionPolicy(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return getPermissionPolicyResponse{Policy: policy}, nil
	})
}

func (h *Handler) deletePermissionPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deletePermissionPolicyRequest) (any, error) {
		if err := h.waf.DeletePermissionPolicy(ctx, req.ResourceArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createAPIKeyRequest) (any, error) {
		apiKey, err := h.waf.CreateAPIKey(ctx, req.Scope, req.TokenDomains)
		if err != nil {
			return nil, err
		}

		return createAPIKeyResponse{APIKey: apiKey}, nil
	})
}

func (h *Handler) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteAPIKeyRequest) (any, error) {
		if err := h.waf.DeleteAPIKey(ctx, req.Scope, req.APIKey); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listAPIKeysRequest) (any, error) {
		keys, err := h.waf.ListAPIKeys(ctx, req.Scope)
		if err != nil {
			return nil, err
		}

		out := make([]apiKeySummaryJSON, 0, len(keys))
		for i := range keys {
			out = append(out, apiKeyToWire(&keys[i]))
		}

		return listAPIKeysResponse{APIKeySummaries: out}, nil
	})
}

func (h *Handler) getDecryptedAPIKey(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getDecryptedAPIKeyRequest) (any, error) {
		summary, err := h.waf.GetDecryptedAPIKey(ctx, req.Scope, req.APIKey)
		if err != nil {
			return nil, err
		}

		domains := summary.TokenDomains
		if domains == nil {
			domains = []string{}
		}

		return getDecryptedAPIKeyResponse{
			CreationTimestamp: float64(summary.Created), TokenDomains: domains,
		}, nil
	})
}
