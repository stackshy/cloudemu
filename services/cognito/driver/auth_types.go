package driver

import "time"

// Auth flows accepted by InitiateAuth and AdminInitiateAuth.
const (
	AuthFlowUserPassword      = "USER_PASSWORD_AUTH"
	AuthFlowAdminUserPassword = "ADMIN_USER_PASSWORD_AUTH"
	AuthFlowAdminNoSRP        = "ADMIN_NO_SRP_AUTH"
	AuthFlowRefreshToken      = "REFRESH_TOKEN"
	AuthFlowRefreshTokenAuth  = "REFRESH_TOKEN_AUTH"
	AuthFlowUserSRP           = "USER_SRP_AUTH"
	AuthFlowCustom            = "CUSTOM_AUTH"
	AuthFlowUser              = "USER_AUTH"
)

// ChallengeNewPasswordRequired is the challenge a FORCE_CHANGE_PASSWORD user
// gets on sign-in.
const ChallengeNewPasswordRequired = "NEW_PASSWORD_REQUIRED"

// Delivery media reported in CodeDeliveryDetails.
const (
	DeliveryMediumEmail = "EMAIL"
	DeliveryMediumSMS   = "SMS"
)

// Group is a user-pool group.
type Group struct {
	GroupName        string
	UserPoolID       string
	Description      string
	RoleARN          string
	Precedence       *int32
	CreationDate     time.Time
	LastModifiedDate time.Time
}

// CreateGroupInput is the input to CreateGroup.
type CreateGroupInput struct {
	UserPoolID  string
	GroupName   string
	Description string
	RoleARN     string
	Precedence  *int32
}

// UpdateGroupInput is the input to UpdateGroup. A nil field is left unchanged.
type UpdateGroupInput struct {
	UserPoolID  string
	GroupName   string
	Description *string
	RoleARN     *string
	Precedence  *int32
}

// ClientUserInput names a user through an app client, with the SECRET_HASH the
// client needs when it has a secret.
type ClientUserInput struct {
	ClientID   string
	SecretHash string
	Username   string
}

// SignUpInput is the input to SignUp.
type SignUpInput struct {
	ClientUserInput
	Password       string
	UserAttributes []Attribute
}

// SignUpOutput is the result of SignUp.
type SignUpOutput struct {
	UserConfirmed       bool
	UserSub             string
	CodeDeliveryDetails *CodeDeliveryDetails
}

// CodeDeliveryDetails describes where a confirmation code was sent. The
// destination is masked the way Cognito masks it.
type CodeDeliveryDetails struct {
	Destination    string
	DeliveryMedium string
	AttributeName  string
}

// ConfirmSignUpInput is the input to ConfirmSignUp.
type ConfirmSignUpInput struct {
	ClientUserInput
	ConfirmationCode   string
	ForceAliasCreation bool
}

// IssuedCode is the confirmation code currently outstanding for a user.
type IssuedCode struct {
	Code      string
	ExpiresAt time.Time
	Delivery  *CodeDeliveryDetails
}

// InitiateAuthInput is the input to InitiateAuth and AdminInitiateAuth.
// UserPoolID is set only by AdminInitiateAuth.
type InitiateAuthInput struct {
	UserPoolID     string
	ClientID       string
	AuthFlow       string
	AuthParameters map[string]string
}

// RespondToAuthChallengeInput is the input to RespondToAuthChallenge and
// AdminRespondToAuthChallenge. UserPoolID is set only by the admin variant.
type RespondToAuthChallengeInput struct {
	UserPoolID         string
	ClientID           string
	ChallengeName      string
	Session            string
	ChallengeResponses map[string]string
}

// AuthResult is a sign-in step: either a challenge to answer or the issued
// tokens.
type AuthResult struct {
	ChallengeName        string
	Session              string
	ChallengeParameters  map[string]string
	AuthenticationResult *AuthenticationResult
}

// AuthenticationResult carries the issued tokens. RefreshToken is empty on a
// refresh.
type AuthenticationResult struct {
	AccessToken  string
	IDToken      string
	RefreshToken string
	ExpiresIn    int32
	TokenType    string
}

// RevokeTokenInput is the input to RevokeToken.
type RevokeTokenInput struct {
	Token        string
	ClientID     string
	ClientSecret string
}
