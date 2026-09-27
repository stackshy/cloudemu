package cognito

import (
	"net/http"
	"strings"
)

// publicOps are the cognito-idp operations whose Smithy model carries
// "auth": ["smithy.api#noAuth"] (botocore: "authtype": "none"), taken from the
// botocore model (awscli 2.31.19, botocore/data/cognito-idp/2016-04-18/service-2.json).
// They authenticate with an access token, a session, or a client secret hash
// inside the request, never with SigV4. TestPublicOpsMatchSDKModel re-derives
// the set from the generated aws-sdk-go-v2 auth resolver and fails on drift.
//
//nolint:gochecknoglobals // static protocol lookup table
var publicOps = map[string]struct{}{
	"AssociateSoftwareToken":           {},
	"ChangePassword":                   {},
	"CompleteWebAuthnRegistration":     {},
	"ConfirmDevice":                    {},
	"ConfirmForgotPassword":            {},
	"ConfirmSignUp":                    {},
	"DeleteUser":                       {},
	"DeleteUserAttributes":             {},
	"DeleteWebAuthnCredential":         {},
	"ForgetDevice":                     {},
	"ForgotPassword":                   {},
	"GetDevice":                        {},
	"GetTokensFromRefreshToken":        {},
	"GetUser":                          {},
	"GetUserAttributeVerificationCode": {},
	"GetUserAuthFactors":               {},
	"GlobalSignOut":                    {},
	"InitiateAuth":                     {},
	"ListDevices":                      {},
	"ListWebAuthnCredentials":          {},
	"ResendConfirmationCode":           {},
	"RespondToAuthChallenge":           {},
	"RevokeToken":                      {},
	"SetUserMFAPreference":             {},
	"SetUserSettings":                  {},
	"SignUp":                           {},
	"StartWebAuthnRegistration":        {},
	"UpdateAuthEventFeedback":          {},
	"UpdateDeviceStatus":               {},
	"UpdateUserAttributes":             {},
	"VerifySoftwareToken":              {},
	"VerifyUserAttribute":              {},
}

// PublicRequest reports whether r is a noAuth cognito-idp operation on the
// normal JSON-RPC route (POST / with the operation's X-Amz-Target). Every other
// request this handler claims, whatever its Host or path, needs SigV4.
func (*Handler) PublicRequest(r *http.Request) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/" {
		return false
	}

	op, ok := strings.CutPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	if !ok {
		return false
	}

	_, public := publicOps[op]

	return public
}
