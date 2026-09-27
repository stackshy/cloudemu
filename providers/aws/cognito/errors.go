package cognito

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// invalidParameter builds an InvalidParameterException-tagged error for bad
// input (missing name, unknown enum value, malformed pagination token).
func invalidParameter(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidParameter, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// resourceNotFound builds a ResourceNotFoundException-tagged error for a missing
// user pool or app client, which the Terraform AWS provider recognizes on its
// delete and refresh paths to treat the resource as gone.
func resourceNotFound(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExResourceNotFound, Err: errors.Newf(errors.NotFound, format, args...)}
}

// poolNotFound is the ResourceNotFoundException for a missing user pool.
func poolNotFound(id string) error {
	return resourceNotFound("User pool %s does not exist.", id)
}

// userNotFound is the UserNotFoundException real Cognito returns for an unknown
// username.
func userNotFound() error {
	//nolint:revive // exact Cognito message, surfaced verbatim to the SDK
	return &driver.APIError{Exception: driver.ExUserNotFound, Err: errors.New(errors.NotFound, "User does not exist.")}
}

// usernameExists builds a UsernameExistsException for a duplicate user.
func usernameExists(msg string) error {
	return &driver.APIError{Exception: driver.ExUsernameExists, Err: errors.New(errors.AlreadyExists, msg)}
}

// aliasExists builds the AliasExistsException for a sign-in value (email, phone
// number or preferred username) that another user already holds.
func aliasExists(attr string) error {
	return &driver.APIError{
		Exception: driver.ExAliasExists,
		Err:       errors.New(errors.AlreadyExists, "An account with the given "+attr+" already exists."),
	}
}

// invalidPassword builds an InvalidPasswordException for a password that breaks
// the pool's policy.
func invalidPassword(reason string) error {
	return &driver.APIError{
		Exception: driver.ExInvalidPassword,
		Err:       errors.New(errors.InvalidArgument, "Password did not conform with policy: "+reason),
	}
}

// notAuthorized builds a NotAuthorizedException for an operation the user's
// current state does not allow.
func notAuthorized(msg string) error {
	return &driver.APIError{Exception: driver.ExNotAuthorized, Err: errors.New(errors.FailedPrecondition, msg)}
}

// unsupportedUserState builds an UnsupportedUserStateException.
func unsupportedUserState(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExUnsupportedUserState, Err: errors.Newf(errors.FailedPrecondition, format, args...)}
}
