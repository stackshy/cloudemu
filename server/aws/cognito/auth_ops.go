package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

type codeDeliveryJSON struct {
	Destination    string `json:"Destination,omitempty"`
	DeliveryMedium string `json:"DeliveryMedium,omitempty"`
	AttributeName  string `json:"AttributeName,omitempty"`
}

func deliveryToWire(d *driver.CodeDeliveryDetails) *codeDeliveryJSON {
	if d == nil {
		return nil
	}

	out := codeDeliveryJSON(*d)

	return &out
}

type signUpRequest struct {
	ClientID       string          `json:"ClientId"`
	SecretHash     string          `json:"SecretHash"`
	Username       string          `json:"Username"`
	Password       string          `json:"Password"`
	UserAttributes []attributeJSON `json:"UserAttributes"`
}

type signUpResponse struct {
	UserConfirmed       bool              `json:"UserConfirmed"`
	UserSub             string            `json:"UserSub"`
	CodeDeliveryDetails *codeDeliveryJSON `json:"CodeDeliveryDetails,omitempty"`
}

func (h *Handler) signUp(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *signUpRequest) (any, error) {
		out, err := h.cognito.SignUp(ctx, driver.SignUpInput{
			ClientUserInput: driver.ClientUserInput{ClientID: req.ClientID, SecretHash: req.SecretHash, Username: req.Username},
			Password:        req.Password,
			UserAttributes:  attributesFromWire(req.UserAttributes),
		})
		if err != nil {
			return nil, err
		}

		return signUpResponse{
			UserConfirmed:       out.UserConfirmed,
			UserSub:             out.UserSub,
			CodeDeliveryDetails: deliveryToWire(out.CodeDeliveryDetails),
		}, nil
	})
}

type clientUserRequest struct {
	ClientID           string `json:"ClientId"`
	SecretHash         string `json:"SecretHash"`
	Username           string `json:"Username"`
	ConfirmationCode   string `json:"ConfirmationCode"`
	ForceAliasCreation bool   `json:"ForceAliasCreation"`
}

func (req *clientUserRequest) ref() driver.ClientUserInput {
	return driver.ClientUserInput{ClientID: req.ClientID, SecretHash: req.SecretHash, Username: req.Username}
}

func (h *Handler) confirmSignUp(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *clientUserRequest) (any, error) {
		err := h.cognito.ConfirmSignUp(ctx, driver.ConfirmSignUpInput{
			ClientUserInput: req.ref(), ConfirmationCode: req.ConfirmationCode, ForceAliasCreation: req.ForceAliasCreation,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type resendResponse struct {
	CodeDeliveryDetails *codeDeliveryJSON `json:"CodeDeliveryDetails,omitempty"`
}

func (h *Handler) resendConfirmationCode(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *clientUserRequest) (any, error) {
		d, err := h.cognito.ResendConfirmationCode(ctx, req.ref())
		if err != nil {
			return nil, err
		}

		return resendResponse{CodeDeliveryDetails: deliveryToWire(d)}, nil
	})
}

func (h *Handler) adminConfirmSignUp(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminConfirmSignUp)
}

func (h *Handler) adminUserGlobalSignOut(w http.ResponseWriter, r *http.Request) {
	h.simpleUserOp(w, r, h.cognito.AdminUserGlobalSignOut)
}

type authenticationResultJSON struct {
	AccessToken  string `json:"AccessToken,omitempty"`
	ExpiresIn    int32  `json:"ExpiresIn"`
	TokenType    string `json:"TokenType,omitempty"`
	RefreshToken string `json:"RefreshToken,omitempty"`
	IDToken      string `json:"IdToken,omitempty"`
}

type authResponse struct {
	ChallengeName        string                    `json:"ChallengeName,omitempty"`
	Session              string                    `json:"Session,omitempty"`
	ChallengeParameters  map[string]string         `json:"ChallengeParameters"`
	AuthenticationResult *authenticationResultJSON `json:"AuthenticationResult,omitempty"`
}

func authToWire(res *driver.AuthResult) authResponse {
	out := authResponse{
		ChallengeName:       res.ChallengeName,
		Session:             res.Session,
		ChallengeParameters: res.ChallengeParameters,
	}

	if out.ChallengeParameters == nil {
		out.ChallengeParameters = map[string]string{}
	}

	if a := res.AuthenticationResult; a != nil {
		out.AuthenticationResult = &authenticationResultJSON{
			AccessToken:  a.AccessToken,
			ExpiresIn:    a.ExpiresIn,
			TokenType:    a.TokenType,
			RefreshToken: a.RefreshToken,
			IDToken:      a.IDToken,
		}
	}

	return out
}

type initiateAuthRequest struct {
	UserPoolID     string            `json:"UserPoolId"`
	ClientID       string            `json:"ClientId"`
	AuthFlow       string            `json:"AuthFlow"`
	AuthParameters map[string]string `json:"AuthParameters"`
}

func (req *initiateAuthRequest) input() driver.InitiateAuthInput {
	return driver.InitiateAuthInput{
		UserPoolID: req.UserPoolID, ClientID: req.ClientID, AuthFlow: req.AuthFlow, AuthParameters: req.AuthParameters,
	}
}

func (h *Handler) initiateAuth(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *initiateAuthRequest) (any, error) {
		in := req.input()
		in.UserPoolID = ""

		return authResult(h.cognito.InitiateAuth(ctx, in))
	})
}

func (h *Handler) adminInitiateAuth(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *initiateAuthRequest) (any, error) {
		return authResult(h.cognito.AdminInitiateAuth(ctx, req.input()))
	})
}

func authResult(res *driver.AuthResult, err error) (any, error) {
	if err != nil {
		return nil, err
	}

	return authToWire(res), nil
}

type respondRequest struct {
	UserPoolID         string            `json:"UserPoolId"`
	ClientID           string            `json:"ClientId"`
	ChallengeName      string            `json:"ChallengeName"`
	Session            string            `json:"Session"`
	ChallengeResponses map[string]string `json:"ChallengeResponses"`
}

func (req *respondRequest) input() driver.RespondToAuthChallengeInput {
	return driver.RespondToAuthChallengeInput{
		UserPoolID: req.UserPoolID, ClientID: req.ClientID, ChallengeName: req.ChallengeName,
		Session: req.Session, ChallengeResponses: req.ChallengeResponses,
	}
}

func (h *Handler) respondToAuthChallenge(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *respondRequest) (any, error) {
		in := req.input()
		in.UserPoolID = ""

		return authResult(h.cognito.RespondToAuthChallenge(ctx, in))
	})
}

func (h *Handler) adminRespondToAuthChallenge(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *respondRequest) (any, error) {
		return authResult(h.cognito.AdminRespondToAuthChallenge(ctx, req.input()))
	})
}

type accessTokenRequest struct {
	AccessToken string `json:"AccessToken"`
}

type getUserResponse struct {
	Username       string          `json:"Username"`
	UserAttributes []attributeJSON `json:"UserAttributes"`
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *accessTokenRequest) (any, error) {
		u, err := h.cognito.GetUser(ctx, req.AccessToken)
		if err != nil {
			return nil, err
		}

		return getUserResponse{Username: u.Username, UserAttributes: attributesToWire(u.Attributes)}, nil
	})
}

func (h *Handler) globalSignOut(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *accessTokenRequest) (any, error) {
		if err := h.cognito.GlobalSignOut(ctx, req.AccessToken); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type revokeTokenRequest struct {
	Token        string `json:"Token"`
	ClientID     string `json:"ClientId"`
	ClientSecret string `json:"ClientSecret"`
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *revokeTokenRequest) (any, error) {
		err := h.cognito.RevokeToken(ctx, driver.RevokeTokenInput{
			Token: req.Token, ClientID: req.ClientID, ClientSecret: req.ClientSecret,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
