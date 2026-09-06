package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

type createUserPoolRequest struct {
	PoolName               string                `json:"PoolName"`
	Policies               *policiesJSON         `json:"Policies"`
	MfaConfiguration       string                `json:"MfaConfiguration"`
	DeletionProtection     string                `json:"DeletionProtection"`
	AutoVerifiedAttributes []string              `json:"AutoVerifiedAttributes"`
	AliasAttributes        []string              `json:"AliasAttributes"`
	UsernameAttributes     []string              `json:"UsernameAttributes"`
	Schema                 []schemaAttributeJSON `json:"Schema"`
	UserPoolTags           map[string]string     `json:"UserPoolTags"`
}

type userPoolResponse struct {
	UserPool userPoolJSON `json:"UserPool"`
}

func (h *Handler) createUserPool(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUserPoolRequest) (any, error) {
		pool, err := h.cognito.CreateUserPool(ctx, driver.CreateUserPoolInput{
			Name:                   req.PoolName,
			Policies:               policiesFromWire(req.Policies),
			MFAConfiguration:       req.MfaConfiguration,
			DeletionProtection:     req.DeletionProtection,
			AutoVerifiedAttributes: req.AutoVerifiedAttributes,
			AliasAttributes:        req.AliasAttributes,
			UsernameAttributes:     req.UsernameAttributes,
			SchemaAttributes:       schemaAttributesFromWire(req.Schema),
			UserPoolTags:           req.UserPoolTags,
		})
		if err != nil {
			return nil, err
		}

		return userPoolResponse{UserPool: userPoolToWire(pool)}, nil
	})
}

type describeUserPoolRequest struct {
	UserPoolID string `json:"UserPoolId"`
}

func (h *Handler) describeUserPool(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeUserPoolRequest) (any, error) {
		pool, err := h.cognito.DescribeUserPool(ctx, req.UserPoolID)
		if err != nil {
			return nil, err
		}

		return userPoolResponse{UserPool: userPoolToWire(pool)}, nil
	})
}

type updateUserPoolRequest struct {
	UserPoolID             string            `json:"UserPoolId"`
	Policies               *policiesJSON     `json:"Policies"`
	MfaConfiguration       string            `json:"MfaConfiguration"`
	DeletionProtection     string            `json:"DeletionProtection"`
	AutoVerifiedAttributes []string          `json:"AutoVerifiedAttributes"`
	UserPoolTags           map[string]string `json:"UserPoolTags"`
}

func (h *Handler) updateUserPool(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateUserPoolRequest) (any, error) {
		err := h.cognito.UpdateUserPool(ctx, driver.UpdateUserPoolInput{
			ID:                     req.UserPoolID,
			Policies:               policiesFromWire(req.Policies),
			MFAConfiguration:       req.MfaConfiguration,
			DeletionProtection:     req.DeletionProtection,
			AutoVerifiedAttributes: req.AutoVerifiedAttributes,
			UserPoolTags:           req.UserPoolTags,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteUserPoolRequest struct {
	UserPoolID string `json:"UserPoolId"`
}

func (h *Handler) deleteUserPool(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteUserPoolRequest) (any, error) {
		if err := h.cognito.DeleteUserPool(ctx, req.UserPoolID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listUserPoolsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listUserPoolsResponse struct {
	UserPools []userPoolDescriptionJSON `json:"UserPools"`
	NextToken string                    `json:"NextToken,omitempty"`
}

func (h *Handler) listUserPools(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listUserPoolsRequest) (any, error) {
		pools, next, err := h.cognito.ListUserPools(ctx,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]userPoolDescriptionJSON, 0, len(pools))

		for i := range pools {
			p := pools[i]
			out = append(out, userPoolDescriptionJSON{
				ID:               p.ID,
				Name:             p.Name,
				CreationDate:     epochOrNil(p.CreationDate),
				LastModifiedDate: epochOrNil(p.LastModifiedDate),
			})
		}

		return listUserPoolsResponse{UserPools: out, NextToken: next}, nil
	})
}
