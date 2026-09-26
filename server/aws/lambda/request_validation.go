package lambda

import (
	"fmt"
	"net/http"
	"regexp"
)

// roleARNPattern is the Role constraint from the Lambda API model.
const roleARNPattern = `arn:(aws[a-zA-Z-]*)?:iam::\d{12}:role/?[a-zA-Z_0-9+=,.@\-_/]+`

// envKeyPattern is the environment variable key constraint from the Lambda
// API model. A key starts with a letter and has at least two characters.
const envKeyPattern = `[a-zA-Z]([a-zA-Z0-9_])+`

var (
	roleARNRe = regexp.MustCompile(`^` + roleARNPattern + `$`) //nolint:gocritic // copied verbatim from the API model.
	envKeyRe  = regexp.MustCompile(`^` + envKeyPattern + `$`)
)

// checkModelConstraints runs the input checks real Lambda does before the
// request reaches the service. It writes a ValidationException and returns
// false on the first failure. An empty role is skipped because
// UpdateFunctionConfiguration makes it optional.
func checkModelConstraints(w http.ResponseWriter, role string, env *envEnvelope) bool {
	msg := modelConstraintMessage(role, env)
	if msg == "" {
		return true
	}

	writeError(w, http.StatusBadRequest, "ValidationException", msg)

	return false
}

// modelConstraintMessage returns the ValidationException text for the first
// failed check, or "" when the input is valid.
func modelConstraintMessage(role string, env *envEnvelope) string {
	if role != "" && !roleARNRe.MatchString(role) {
		return fmt.Sprintf("1 validation error detected: Value '%s' at 'role' failed to satisfy constraint: "+
			"Member must satisfy regular expression pattern: %s", role, roleARNPattern)
	}

	if env == nil {
		return ""
	}

	for k := range env.Variables {
		if !envKeyRe.MatchString(k) {
			return "1 validation error detected: Value at 'environment.variables' failed to satisfy constraint: " +
				"Map keys must satisfy constraint: [Member must satisfy regular expression pattern: " + envKeyPattern + "]"
		}
	}

	return ""
}
