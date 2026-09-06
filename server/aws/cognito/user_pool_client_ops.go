package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// createUserPoolClientRequest carries the app-client settings for create and
// (with ClientId set) update.
type createUserPoolClientRequest struct {
	UserPoolID                      string                  `json:"UserPoolId"`
	ClientName                      string                  `json:"ClientName"`
	ClientID                        string                  `json:"ClientId"`
	GenerateSecret                  bool                    `json:"GenerateSecret"`
	RefreshTokenValidity            *int32                  `json:"RefreshTokenValidity"`
	AccessTokenValidity             *int32                  `json:"AccessTokenValidity"`
	IDTokenValidity                 *int32                  `json:"IdTokenValidity"`
	TokenValidityUnits              *tokenValidityUnitsJSON `json:"TokenValidityUnits"`
	ExplicitAuthFlows               []string                `json:"ExplicitAuthFlows"`
	AuthSessionValidity             *int32                  `json:"AuthSessionValidity"`
	EnableTokenRevocation           *bool                   `json:"EnableTokenRevocation"`
	PreventUserExistenceErrors      string                  `json:"PreventUserExistenceErrors"`
	CallbackURLs                    []string                `json:"CallbackURLs"`
	LogoutURLs                      []string                `json:"LogoutURLs"`
	DefaultRedirectURI              string                  `json:"DefaultRedirectURI"`
	AllowedOAuthFlows               []string                `json:"AllowedOAuthFlows"`
	AllowedOAuthScopes              []string                `json:"AllowedOAuthScopes"`
	AllowedOAuthFlowsUserPoolClient bool                    `json:"AllowedOAuthFlowsUserPoolClient"`
	SupportedIdentityProviders      []string                `json:"SupportedIdentityProviders"`
	ReadAttributes                  []string                `json:"ReadAttributes"`
	WriteAttributes                 []string                `json:"WriteAttributes"`
}

func (req *createUserPoolClientRequest) toInput() driver.CreateUserPoolClientInput {
	return driver.CreateUserPoolClientInput{
		UserPoolID:                      req.UserPoolID,
		ClientName:                      req.ClientName,
		ClientID:                        req.ClientID,
		GenerateSecret:                  req.GenerateSecret,
		RefreshTokenValidity:            req.RefreshTokenValidity,
		AccessTokenValidity:             req.AccessTokenValidity,
		IDTokenValidity:                 req.IDTokenValidity,
		TokenValidityUnits:              tokenValidityUnitsFromWire(req.TokenValidityUnits),
		ExplicitAuthFlows:               req.ExplicitAuthFlows,
		AuthSessionValidity:             req.AuthSessionValidity,
		EnableTokenRevocation:           req.EnableTokenRevocation,
		PreventUserExistenceErrors:      req.PreventUserExistenceErrors,
		CallbackURLs:                    req.CallbackURLs,
		LogoutURLs:                      req.LogoutURLs,
		DefaultRedirectURI:              req.DefaultRedirectURI,
		AllowedOAuthFlows:               req.AllowedOAuthFlows,
		AllowedOAuthScopes:              req.AllowedOAuthScopes,
		AllowedOAuthFlowsUserPoolClient: req.AllowedOAuthFlowsUserPoolClient,
		SupportedIdentityProviders:      req.SupportedIdentityProviders,
		ReadAttributes:                  req.ReadAttributes,
		WriteAttributes:                 req.WriteAttributes,
	}
}

type userPoolClientResponse struct {
	UserPoolClient userPoolClientJSON `json:"UserPoolClient"`
}

func (h *Handler) createUserPoolClient(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUserPoolClientRequest) (any, error) {
		client, err := h.cognito.CreateUserPoolClient(ctx, req.toInput())
		if err != nil {
			return nil, err
		}

		return userPoolClientResponse{UserPoolClient: userPoolClientToWire(client)}, nil
	})
}

func (h *Handler) updateUserPoolClient(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUserPoolClientRequest) (any, error) {
		client, err := h.cognito.UpdateUserPoolClient(ctx, req.toInput())
		if err != nil {
			return nil, err
		}

		return userPoolClientResponse{UserPoolClient: userPoolClientToWire(client)}, nil
	})
}

type describeUserPoolClientRequest struct {
	UserPoolID string `json:"UserPoolId"`
	ClientID   string `json:"ClientId"`
}

func (h *Handler) describeUserPoolClient(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeUserPoolClientRequest) (any, error) {
		client, err := h.cognito.DescribeUserPoolClient(ctx, req.UserPoolID, req.ClientID)
		if err != nil {
			return nil, err
		}

		return userPoolClientResponse{UserPoolClient: userPoolClientToWire(client)}, nil
	})
}

func (h *Handler) deleteUserPoolClient(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeUserPoolClientRequest) (any, error) {
		if err := h.cognito.DeleteUserPoolClient(ctx, req.UserPoolID, req.ClientID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listUserPoolClientsRequest struct {
	UserPoolID string `json:"UserPoolId"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listUserPoolClientsResponse struct {
	UserPoolClients []userPoolClientDescriptionJSON `json:"UserPoolClients"`
	NextToken       string                          `json:"NextToken,omitempty"`
}

func (h *Handler) listUserPoolClients(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listUserPoolClientsRequest) (any, error) {
		clients, next, err := h.cognito.ListUserPoolClients(ctx, req.UserPoolID,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]userPoolClientDescriptionJSON, 0, len(clients))
		for _, c := range clients {
			out = append(out, userPoolClientDescriptionJSON{
				ClientID:   c.ClientID,
				ClientName: c.ClientName,
				UserPoolID: c.UserPoolID,
			})
		}

		return listUserPoolClientsResponse{UserPoolClients: out, NextToken: next}, nil
	})
}
