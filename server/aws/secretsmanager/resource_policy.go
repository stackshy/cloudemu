package secretsmanager

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// effectAllow is the policy Effect that grants (rather than denies) access; only
// an Allow statement can widen access to the public.
const effectAllow = "Allow"

// getResourcePolicy returns the secret's ARN/Name and its resource policy (the
// ResourcePolicy field is omitted when none is set, which the SDK reads as "no
// policy"). Backing drivers without the policy surface report no policy.
func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req secretIDRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	name := resolveSecretID(req.SecretID)

	pm, ok := h.secrets.(secretPolicyManager)
	if !ok {
		info, err := h.secrets.GetSecret(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		wire.WriteJSON(w, getResourcePolicyResponse{ARN: info.ResourceID, Name: info.Name})

		return
	}

	info, policy, err := pm.GetResourcePolicy(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, getResourcePolicyResponse{ARN: info.ResourceID, Name: info.Name, ResourcePolicy: policy})
}

// putResourcePolicy attaches a JSON resource-based policy to the secret, the
// write path Terraform's aws_secretsmanager_secret_policy uses. A malformed
// policy is rejected as MalformedPolicyDocumentException; a public policy with
// BlockPublicPolicy set is rejected as PublicPolicyException.
func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.secrets.(secretPolicyManager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	var req putResourcePolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if !isValidPolicyJSON(req.ResourcePolicy) {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"MalformedPolicyDocumentException", "the resource policy is not valid JSON")

		return
	}

	if req.BlockPublicPolicy && policyGrantsPublicAccess(req.ResourcePolicy) {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"PublicPolicyException", "the resource policy grants broad access and BlockPublicPolicy is set")

		return
	}

	info, err := pm.PutResourcePolicy(r.Context(), resolveSecretID(req.SecretID), req.ResourcePolicy)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, secretRefResponse{ARN: info.ResourceID, Name: info.Name})
}

// deleteResourcePolicy clears the secret's resource policy.
func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	pm, ok := h.secrets.(secretPolicyManager)
	if !ok {
		writeErr(w, errNotSupported)
		return
	}

	respondSecretRef(w, r, pm.DeleteResourcePolicy)
}

// validateResourcePolicy checks a policy's JSON syntax and whether it grants
// broad access, without storing it. A malformed or public policy returns
// PolicyValidationPassed=false with the specific ValidationErrors.
func (*Handler) validateResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req validateResourcePolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	errs := validatePolicy(req.ResourcePolicy)

	wire.WriteJSON(w, validateResourcePolicyResponse{
		PolicyValidationPassed: len(errs) == 0,
		ValidationErrors:       errs,
	})
}

// validatePolicy returns the validation errors for a resource policy: a syntax
// error when it is not valid JSON, and a broad-access error when it grants
// public access. An empty result means the policy passed.
func validatePolicy(policy string) []validationErrorEntry {
	if !isValidPolicyJSON(policy) {
		return []validationErrorEntry{{
			CheckName:    "SYNTAX",
			ErrorMessage: "The resource policy is not valid JSON.",
		}}
	}

	if policyGrantsPublicAccess(policy) {
		return []validationErrorEntry{{
			CheckName:    "PUBLIC_ACCESS",
			ErrorMessage: "The resource policy grants broad access to the secret.",
		}}
	}

	return nil
}

// isValidPolicyJSON reports whether policy parses as a JSON object.
func isValidPolicyJSON(policy string) bool {
	var doc map[string]json.RawMessage

	return json.Unmarshal([]byte(policy), &doc) == nil
}

// policyStatement is the subset of an IAM statement needed to detect a public
// grant: an Allow with a wildcard Principal and no Condition narrowing it.
type policyStatement struct {
	Effect    string          `json:"Effect"`
	Principal json.RawMessage `json:"Principal"`
	Condition json.RawMessage `json:"Condition"`
}

// policyGrantsPublicAccess reports whether any statement grants public access —
// Effect Allow, a wildcard ("*") Principal, and no Condition. It mirrors the
// broad-access check AWS runs for BlockPublicPolicy/ValidateResourcePolicy.
func policyGrantsPublicAccess(policy string) bool {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if json.Unmarshal([]byte(policy), &doc) != nil {
		return false
	}

	for _, s := range parseStatements(doc.Statement) {
		if s.Effect == effectAllow && len(s.Condition) == 0 && principalIsWildcard(s.Principal) {
			return true
		}
	}

	return false
}

// parseStatements decodes a Statement that may be a single object or an array
// of objects into a slice.
func parseStatements(raw json.RawMessage) []policyStatement {
	if len(raw) == 0 {
		return nil
	}

	var many []policyStatement
	if json.Unmarshal(raw, &many) == nil {
		return many
	}

	var one policyStatement
	if json.Unmarshal(raw, &one) == nil {
		return []policyStatement{one}
	}

	return nil
}

// principalIsWildcard reports whether a Principal grants everyone: the string
// "*", or a map whose values include "*" (e.g. {"AWS":"*"}).
func principalIsWildcard(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}

	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString == "*"
	}

	var asMap map[string]json.RawMessage
	if json.Unmarshal(raw, &asMap) != nil {
		return false
	}

	for _, v := range asMap {
		if rawContainsWildcard(v) {
			return true
		}
	}

	return false
}

// rawContainsWildcard reports whether a principal map value ("*", or a list
// containing "*") is a wildcard.
func rawContainsWildcard(raw json.RawMessage) bool {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString == "*"
	}

	var asList []string
	if json.Unmarshal(raw, &asList) == nil {
		for _, v := range asList {
			if v == "*" {
				return true
			}
		}
	}

	return false
}
