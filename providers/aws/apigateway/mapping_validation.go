package apigateway

import (
	"regexp"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/vtl"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Parameter-mapping expressions API Gateway accepts.
var (
	methodRequestParamKey  = regexp.MustCompile(`^method\.request\.` + paramLocations + `\.\S+$`)
	methodResponseParamKey = regexp.MustCompile(`^method\.response\.header\.\S+$`)
	integrationRequestKey  = regexp.MustCompile(`^integration\.request\.` + paramLocations + `\.\S+$`)
	integrationRespSource  = regexp.MustCompile(
		`^integration\.response\.((header|multivalueheader)\.\S+|body(\..+)?)$`)
	commonSource = regexp.MustCompile(`^('[^']*'|stageVariables\.\S+|context\.\S+)$`)
	methodBody   = regexp.MustCompile(`^method\.request\.body(\..+)?$`)
)

// paramLocations are the request parameter locations a mapping can name.
const paramLocations = `(path|querystring|multivaluequerystring|header|multivalueheader)`

const (
	mappingErrPrefix       = "Invalid mapping expression specified: Validation Result: warnings : [], errors : ["
	msgBadThroughBehavior  = "Invalid passthrough behavior specified"
	msgInvalidSelection    = "Invalid selection pattern specified"
	msgTemplateTooLarge    = "Mapping template for content type %s exceeds the maximum size of 300 KB"
	msgContentHandlingEnum = "1 validation error detected: Value '%s' at 'contentHandling' failed to satisfy " +
		"constraint: Member must satisfy enum value set: [CONVERT_TO_BINARY, CONVERT_TO_TEXT]"
)

func invalidExpression(expr string) error {
	return cerrors.New(cerrors.InvalidArgument, mappingErrPrefix+"Invalid mapping expression specified: "+expr+"]")
}

func invalidParameter(param string) error {
	return cerrors.New(cerrors.InvalidArgument, mappingErrPrefix+"Invalid mapping expression parameter specified: "+param+"]")
}

// validateMethodRequestParams checks method.request.{location}.{name} keys.
func validateMethodRequestParams(params map[string]bool) error {
	for _, k := range sortedBoolKeys(params) {
		if !methodRequestParamKey.MatchString(k) {
			return invalidExpression(k)
		}
	}

	return nil
}

// validateMethodResponseParams checks method.response.header.{name} keys.
func validateMethodResponseParams(params map[string]bool) error {
	for _, k := range sortedBoolKeys(params) {
		if !methodResponseParamKey.MatchString(k) {
			return invalidExpression(k)
		}
	}

	return nil
}

func validateContentHandling(v string) error {
	switch v {
	case "", "CONVERT_TO_BINARY", "CONVERT_TO_TEXT":
		return nil
	default:
		return cerrors.Newf(cerrors.InvalidArgument, msgContentHandlingEnum, v)
	}
}

// validateIntegrationSettings checks an integration's passthrough behavior,
// content handling and request parameter mappings. A method.request source
// must be declared on the method.
func validateIntegrationSettings(ig *driver.Integration, methodParams map[string]bool) error {
	switch ig.PassthroughBehavior {
	case driver.PassthroughWhenNoMatch, driver.PassthroughWhenNoTemplates, driver.PassthroughNever:
	default:
		return cerrors.New(cerrors.InvalidArgument, msgBadThroughBehavior)
	}

	if err := validateContentHandling(ig.ContentHandling); err != nil {
		return err
	}

	if err := validateTemplateSizes(ig.RequestTemplates); err != nil {
		return err
	}

	for _, k := range sortedKeys(ig.RequestParameters) {
		if !integrationRequestKey.MatchString(k) {
			return invalidParameter(k)
		}

		src := ig.RequestParameters[k]

		switch {
		case commonSource.MatchString(src), methodBody.MatchString(src):
		case methodRequestParamKey.MatchString(src):
			if _, ok := methodParams[src]; !ok {
				return invalidParameter(src)
			}
		default:
			return invalidExpression(src)
		}
	}

	return nil
}

// validateIntegrationResponse checks the selection pattern and that every
// response parameter targets a header the method response declares.
func validateIntegrationResponse(ir *driver.IntegrationResponse, mr *driver.MethodResponse) error {
	if _, err := regexp.Compile(ir.SelectionPattern); err != nil {
		return cerrors.New(cerrors.InvalidArgument, msgInvalidSelection)
	}

	if err := validateTemplateSizes(ir.ResponseTemplates); err != nil {
		return err
	}

	for _, k := range sortedKeys(ir.ResponseParameters) {
		declared := mr != nil && mr.ResponseParameters != nil
		if declared {
			_, declared = mr.ResponseParameters[k]
		}

		if !methodResponseParamKey.MatchString(k) || !declared {
			return invalidParameter(k)
		}

		src := ir.ResponseParameters[k]
		if !commonSource.MatchString(src) && !integrationRespSource.MatchString(src) {
			return invalidExpression(src)
		}
	}

	return nil
}

// validateTemplateSizes enforces API Gateway's 300 KB mapping-template quota.
func validateTemplateSizes(templates map[string]string) error {
	for _, ct := range sortedKeys(templates) {
		if len(templates[ct]) > vtl.MaxTemplateBytes {
			return cerrors.Newf(cerrors.InvalidArgument, msgTemplateTooLarge, ct)
		}
	}

	return nil
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// headerName returns the {name} of a method.response.header.{name} key.
func headerName(key string) string {
	return strings.TrimPrefix(key, "method.response.header.")
}
