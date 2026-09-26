package lambda

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// maxEnvironmentBytes is the 4 KB cap on a function's environment variables.
const maxEnvironmentBytes = 4096

// reservedEnvKeys are the keys the Lambda runtime sets itself, so a function
// configuration cannot set them. The list follows the Lambda developer guide.
//
//nolint:gochecknoglobals // read-only lookup table.
var reservedEnvKeys = map[string]bool{
	"_HANDLER":                        true,
	"_X_AMZN_TRACE_ID":                true,
	"AWS_DEFAULT_REGION":              true,
	"AWS_REGION":                      true,
	"AWS_EXECUTION_ENV":               true,
	"AWS_LAMBDA_FUNCTION_NAME":        true,
	"AWS_LAMBDA_FUNCTION_MEMORY_SIZE": true,
	"AWS_LAMBDA_FUNCTION_VERSION":     true,
	"AWS_LAMBDA_INITIALIZATION_TYPE":  true,
	"AWS_LAMBDA_LOG_GROUP_NAME":       true,
	"AWS_LAMBDA_LOG_STREAM_NAME":      true,
	"AWS_ACCESS_KEY":                  true,
	"AWS_ACCESS_KEY_ID":               true,
	"AWS_SECRET_ACCESS_KEY":           true,
	"AWS_SESSION_TOKEN":               true,
	"AWS_LAMBDA_RUNTIME_API":          true,
	"LAMBDA_TASK_ROOT":                true,
	"LAMBDA_RUNTIME_DIR":              true,
	"AWS_LAMBDA_MAX_CONCURRENCY":      true,
	"AWS_LAMBDA_METADATA_API":         true,
	"AWS_LAMBDA_METADATA_TOKEN":       true,
}

// validateEnvironment rejects reserved keys and an environment larger than
// 4 KB. Both are InvalidArgument, which the wire maps to
// InvalidParameterValueException.
func validateEnvironment(env map[string]string) error {
	if len(env) == 0 {
		return nil
	}

	var reserved []string

	for k := range env {
		if reservedEnvKeys[k] {
			reserved = append(reserved, k)
		}
	}

	if len(reserved) > 0 {
		sort.Strings(reserved)

		return cerrors.Newf(cerrors.InvalidArgument,
			"Lambda was unable to configure your environment variables because the environment variables "+
				"you have provided contains reserved keys that are currently not supported for modification. "+
				"Reserved keys used in this request: %s", strings.Join(reserved, ","))
	}

	measured := encodeEnvironment(env)
	if len(measured) > maxEnvironmentBytes {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Lambda was unable to configure your environment variables because the environment variables "+
				"you have provided exceeded the 4KB limit. String measured: %s", measured)
	}

	return nil
}

// encodeEnvironment renders env as the JSON object Lambda measures.
func encodeEnvironment(env map[string]string) string {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(env); err != nil {
		return ""
	}

	return strings.TrimSuffix(buf.String(), "\n")
}
