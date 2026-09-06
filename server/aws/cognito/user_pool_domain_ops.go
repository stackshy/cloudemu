package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

type createUserPoolDomainRequest struct {
	Domain     string `json:"Domain"`
	UserPoolID string `json:"UserPoolId"`
}

type createUserPoolDomainResponse struct {
	CloudFrontDomain string `json:"CloudFrontDomain,omitempty"`
}

func (h *Handler) createUserPoolDomain(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUserPoolDomainRequest) (any, error) {
		if err := h.cognito.CreateUserPoolDomain(ctx, driver.CreateUserPoolDomainInput{
			Domain:     req.Domain,
			UserPoolID: req.UserPoolID,
		}); err != nil {
			return nil, err
		}

		domain, err := h.cognito.DescribeUserPoolDomain(ctx, req.Domain)
		if err != nil {
			return nil, err
		}

		return createUserPoolDomainResponse{CloudFrontDomain: domain.CloudFrontDistribution}, nil
	})
}

type describeUserPoolDomainRequest struct {
	Domain string `json:"Domain"`
}

type describeUserPoolDomainResponse struct {
	DomainDescription domainDescriptionJSON `json:"DomainDescription"`
}

func (h *Handler) describeUserPoolDomain(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeUserPoolDomainRequest) (any, error) {
		domain, err := h.cognito.DescribeUserPoolDomain(ctx, req.Domain)
		if err != nil {
			return nil, err
		}

		return describeUserPoolDomainResponse{DomainDescription: domainToWire(domain)}, nil
	})
}

type deleteUserPoolDomainRequest struct {
	Domain     string `json:"Domain"`
	UserPoolID string `json:"UserPoolId"`
}

func (h *Handler) deleteUserPoolDomain(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteUserPoolDomainRequest) (any, error) {
		if err := h.cognito.DeleteUserPoolDomain(ctx, req.Domain, req.UserPoolID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
