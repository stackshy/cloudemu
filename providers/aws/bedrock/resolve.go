package bedrock

import (
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// Runtime error texts, matched to what AWS returns. AWS does not document
// them, so they come from observed responses.
const (
	msgInvalidModelID = "The provided model identifier is invalid."
	msgOnDemandFmt    = "Invocation of model ID %s with on-demand throughput isn’t supported."
	msgRetryProfile   = " Retry your request with the ID or ARN of an inference profile that contains this model."
)

// resolvedModel is the foundation model an invocation target runs on. direct
// is true when the caller named the foundation model itself, not a profile,
// custom model or provisioned throughput.
type resolvedModel struct {
	fm     *driver.FoundationModel
	direct bool
}

// resolveModel maps any model identifier a runtime call accepts to the
// foundation model behind it. It returns nil for an unknown identifier.
func (m *Mock) resolveModel(id string) *resolvedModel {
	if fm := m.findFoundation(id); fm != nil {
		return &resolvedModel{fm: fm, direct: true}
	}

	for _, step := range []func(string) (*resolvedModel, bool){
		m.fromCustomModel, m.fromSystemProfile, m.fromAppProfile, m.fromProvisioned,
	} {
		if r, ok := step(id); ok {
			return r
		}
	}

	return nil
}

// fromCustomModel resolves a custom model name or ARN to its base model.
func (m *Mock) fromCustomModel(id string) (*resolvedModel, bool) {
	cm := m.findCustom(id)
	if cm == nil {
		return nil, false
	}

	return &resolvedModel{fm: m.findFoundation(cm.BaseModelARN)}, true
}

// fromSystemProfile resolves a SYSTEM_DEFINED profile ID or ARN.
func (m *Mock) fromSystemProfile(id string) (*resolvedModel, bool) {
	p := m.findSystemProfile(id)
	if p == nil {
		return nil, false
	}

	return &resolvedModel{fm: m.firstFoundation(p.Models)}, true
}

// fromAppProfile resolves an application profile ID or ARN.
func (m *Mock) fromAppProfile(id string) (*resolvedModel, bool) {
	p := m.findAppProfile(id)
	if p == nil {
		return nil, false
	}

	return &resolvedModel{fm: m.firstFoundation(p.Models)}, true
}

// fromProvisioned resolves a provisioned throughput name or ARN to the model
// it serves, which is a foundation or custom model.
func (m *Mock) fromProvisioned(id string) (*resolvedModel, bool) {
	pt := m.findProvisioned(id)
	if pt == nil {
		return nil, false
	}

	if fm := m.findFoundation(pt.ModelARN); fm != nil {
		return &resolvedModel{fm: fm}, true
	}

	if r, ok := m.fromCustomModel(pt.ModelARN); ok {
		return r, true
	}

	return &resolvedModel{}, true
}

// firstFoundation returns the first of arns that names a catalog model. Model
// ARNs in other regions match by model ID.
func (m *Mock) firstFoundation(arns []string) *driver.FoundationModel {
	for _, arn := range arns {
		if fm := m.findFoundation(arn); fm != nil {
			return fm
		}

		if i := strings.LastIndex(arn, "foundation-model/"); i >= 0 {
			if fm := m.findFoundation(arn[i+len("foundation-model/"):]); fm != nil {
				return fm
			}
		}
	}

	return nil
}

// resolveForInvoke resolves id for InvokeModel and Converse. A foundation
// model named directly must support on-demand throughput.
func (m *Mock) resolveForInvoke(id string) (*resolvedModel, error) {
	if id == "" {
		return nil, errors.New(errors.InvalidArgument, "modelId is required")
	}

	r := m.resolveModel(id)
	if r == nil {
		return nil, errors.New(errors.InvalidArgument, msgInvalidModelID)
	}

	if err := checkOnDemand(r, id); err != nil {
		return nil, err
	}

	return r, nil
}

// checkOnDemand rejects a directly named model that has no on-demand
// throughput. Profile-only models point the caller at an inference profile.
func checkOnDemand(r *resolvedModel, id string) error {
	if !r.direct || contains(r.fm.InferenceTypesSupported, driver.InferenceTypeOnDemand) {
		return nil
	}

	msg := fmt.Sprintf(msgOnDemandFmt, id)
	if contains(r.fm.InferenceTypesSupported, driver.InferenceTypeInferenceProfile) {
		msg += msgRetryProfile
	}

	return errors.New(errors.InvalidArgument, msg)
}

// modelID returns the resolved foundation model ID, or "" when unknown.
func (r *resolvedModel) modelID() string {
	if r.fm == nil {
		return ""
	}

	return r.fm.ModelID
}

// isEmbedding reports whether the resolved model returns a vector.
func (r *resolvedModel) isEmbedding() bool {
	return r.fm != nil && contains(r.fm.OutputModalities, driver.ModalityEmbedding)
}

// isImage reports whether the resolved model returns images.
func (r *resolvedModel) isImage() bool {
	return r.fm != nil && contains(r.fm.OutputModalities, driver.ModalityImage)
}

// familyOf classifies a foundation model ID by provider prefix for response
// shaping.
func familyOf(modelID string) string {
	prefixes := []struct{ prefix, family string }{
		{"anthropic.", familyAnthropic},
		{"amazon.titan", familyTitan},
		{"amazon.nova", familyNova},
		{"meta.llama", familyLlama},
		{"cohere.command-r", familyCohereR},
		{"cohere.", familyCohere},
		{"mistral.", familyMistral},
		{"deepseek.", familyDeepSeek},
	}

	for _, p := range prefixes {
		if strings.HasPrefix(modelID, p.prefix) {
			return p.family
		}
	}

	return familyGeneric
}
