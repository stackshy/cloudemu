package apigateway

import (
	"net/url"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Error messages real API Gateway returns for the validations below.
const (
	msgResourceNotFound    = "Invalid Resource identifier specified"
	msgMethodNotFound      = "Invalid Method identifier specified"
	msgIntegrationNotFound = "Invalid Integration identifier specified"
	msgDeploymentNotFound  = "Invalid Deployment identifier specified"
	msgInvalidHTTPMethod   = "Invalid HTTP method specified"
	msgMethodExists        = "Method already exists for this resource"
	msgStageExists         = "Stage already exists"
	msgStageName           = "Stage name only allows a-zA-Z0-9_-"
	msgNoMethods           = "The REST API doesn't contain any methods"
	msgAuthorizationType   = "Invalid authorization type specified"
	msgStageVariableName   = "Stage variable names may only contain alphanumeric characters and underscores"
	msgStageVariableValue  = "Stage variable values must match the regular expression [A-Za-z0-9-._~:/?#&=,]+"
	msgIntegrationTimeout  = "Timeout should be between 50 ms and 29000 ms"
	msgNoIntegration       = "No integration defined for method"
	msgEmptyHTTPMethod     = "Enumeration value for HttpMethod must be non-empty"
	msgInvalidHTTPEndpoint = "Invalid HTTP endpoint specified for URI"
	msgInvalidARN          = "Invalid ARN specified in the request"
	msgARNPathOrAction     = "AWS ARN for integration must contain path or action"
	msgAWSProxyTarget      = "Integrations of type 'AWS_PROXY' currently only supports " +
		"Lambda function and Firehose stream invocations."
	msgIntegrationTypeFmt = "1 validation error detected: Value '%s' at 'putIntegrationInput.type' " +
		"failed to satisfy constraint: Member must satisfy enum value set: [HTTP, AWS, MOCK, HTTP_PROXY, AWS_PROXY]"
)

// maxStageNameLen is the longest stage name API Gateway accepts.
const maxStageNameLen = 128

// Integration timeout bounds in milliseconds, as documented for
// PutIntegration's timeoutInMillis.
const (
	minIntegrationTimeoutMillis = 50
	maxIntegrationTimeoutMillis = 29000
)

// stageVariableName and stageVariableValue are the documented stage variable
// rules: names are alphanumerics and underscores, values match
// [A-Za-z0-9-._~:/?#&=,]+.
var (
	stageVariableName  = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	stageVariableValue = regexp.MustCompile(`^[A-Za-z0-9\-._~:/?#&=,]+$`)
)

// validateStageVariable checks one stage variable against the documented rules.
func validateStageVariable(name, value string) error {
	if !stageVariableName.MatchString(name) {
		return cerrors.New(cerrors.InvalidArgument, msgStageVariableName)
	}

	if !stageVariableValue.MatchString(value) {
		return cerrors.New(cerrors.InvalidArgument, msgStageVariableValue)
	}

	return nil
}

// validateStageVariables checks every stage variable in vars.
func validateStageVariables(vars map[string]string) error {
	for _, k := range sortedKeys(vars) {
		if err := validateStageVariable(k, vars[k]); err != nil {
			return err
		}
	}

	return nil
}

// validAuthorizationType reports whether t is an authorization type a method
// accepts: NONE, AWS_IAM, CUSTOM or COGNITO_USER_POOLS.
func validAuthorizationType(t string) bool {
	switch t {
	case "NONE", "AWS_IAM", "CUSTOM", "COGNITO_USER_POOLS":
		return true
	default:
		return false
	}
}

// validHTTPMethod reports whether method is one PutMethod accepts.
func validHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", driver.MethodANY:
		return true
	default:
		return false
	}
}

// validateStageName enforces the stage-name rule: alphanumerics, hyphens and
// underscores, at most 128 characters.
func validateStageName(name string) error {
	if name == "" || len(name) > maxStageNameLen {
		return cerrors.New(cerrors.InvalidArgument, msgStageName)
	}

	for _, c := range name {
		if !isStageNameChar(c) {
			return cerrors.New(cerrors.InvalidArgument, msgStageName)
		}
	}

	return nil
}

func isStageNameChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// validateIntegration applies PutIntegration's type, httpMethod and uri rules.
func validateIntegration(in *driver.PutIntegrationInput) error {
	switch in.Type {
	case driver.IntegrationMock:
		return nil
	case driver.IntegrationHTTP, driver.IntegrationHTTPProxy, driver.IntegrationAWS, driver.IntegrationAWSProxy:
	default:
		return cerrors.Newf(cerrors.InvalidArgument, msgIntegrationTypeFmt, in.Type)
	}

	if in.IntegrationHTTPMethod == "" {
		return cerrors.New(cerrors.InvalidArgument, msgEmptyHTTPMethod)
	}

	if in.Type == driver.IntegrationHTTP || in.Type == driver.IntegrationHTTPProxy {
		return validateHTTPEndpoint(in.URI)
	}

	return validateAWSIntegrationARN(in.Type, in.URI)
}

// validateHTTPEndpoint requires an absolute http(s) URL. Stage-variable
// placeholders are allowed anywhere, including the host.
func validateHTTPEndpoint(uri string) error {
	u, err := url.Parse(substituteStageVariables(uri, nil, "x"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return cerrors.New(cerrors.InvalidArgument, msgInvalidHTTPEndpoint)
	}

	return nil
}

// validateAWSIntegrationARN checks an AWS/AWS_PROXY integration uri of the form
// arn:aws:apigateway:{region}:{service}:{path|action}/{...}.
func validateAWSIntegrationARN(integrationType, uri string) error {
	const arnParts = 6

	parts := strings.SplitN(uri, ":", arnParts)
	if len(parts) != arnParts || parts[0] != "arn" || parts[2] != "apigateway" {
		return cerrors.New(cerrors.InvalidArgument, msgInvalidARN)
	}

	service, target := parts[4], parts[5]
	if !strings.HasPrefix(target, "path/") && !strings.HasPrefix(target, "action/") {
		return cerrors.New(cerrors.InvalidArgument, msgARNPathOrAction)
	}

	if integrationType == driver.IntegrationAWSProxy && service != "lambda" && service != "firehose" {
		return cerrors.New(cerrors.InvalidArgument, msgAWSProxyTarget)
	}

	return nil
}

// validateDeployable rejects a CreateDeployment of a tree with no methods, or
// with a method that has no integration.
func validateDeployable(resources map[string]*driver.Resource) error {
	methods := 0

	for _, r := range resources {
		for _, mth := range r.Methods {
			if mth.Integration == nil {
				return cerrors.New(cerrors.InvalidArgument, msgNoIntegration)
			}

			methods++
		}
	}

	if methods == 0 {
		return cerrors.New(cerrors.InvalidArgument, msgNoMethods)
	}

	return nil
}

// substituteStageVariables replaces each ${stageVariables.name} in s with the
// stage's value. An unknown name resolves to fallback (empty on invoke, as API
// Gateway does).
func substituteStageVariables(s string, vars map[string]string, fallback string) string {
	const open = "${stageVariables."

	var b strings.Builder

	for {
		i := strings.Index(s, open)
		if i < 0 {
			b.WriteString(s)

			return b.String()
		}

		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			b.WriteString(s)

			return b.String()
		}

		b.WriteString(s[:i])

		if v, ok := vars[s[i+len(open):i+end]]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(fallback)
		}

		s = s[i+end+1:]
	}
}
