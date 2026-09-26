package bedrock

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// modelSpec is one catalog entry before its ARN is filled in.
type modelSpec struct {
	id, name, provider string
	in, out            []string
	stream             bool
	custom, inference  []string
}

// Shared modality and inference-type lists for the catalog. They are copied
// out by cloneFoundationModel, so sharing them is safe.
//
//nolint:gochecknoglobals // immutable catalog building blocks
var (
	mText      = []string{driver.ModalityText}
	mTextImage = []string{driver.ModalityText, driver.ModalityImage}
	mImage     = []string{driver.ModalityImage}
	mEmbed     = []string{driver.ModalityEmbedding}

	onDemand    = []string{driver.InferenceTypeOnDemand}
	onDemandIP  = []string{driver.InferenceTypeOnDemand, driver.InferenceTypeInferenceProfile}
	profileOnly = []string{driver.InferenceTypeInferenceProfile}
	provisioned = []string{driver.InferenceTypeProvisioned}

	ft      = []string{driver.CustomizationFineTuning}
	ftCPT   = []string{driver.CustomizationFineTuning, driver.CustomizationContinuedPreTraining}
	ftDistl = []string{driver.CustomizationFineTuning, driver.CustomizationDistillation}
)

// catalogSpecs is the foundation-model catalog, using real Bedrock model IDs,
// names, providers and inference types.
func catalogSpecs() []modelSpec {
	return []modelSpec{
		{"anthropic.claude-3-sonnet-20240229-v1:0", "Claude 3 Sonnet", "Anthropic", mTextImage, mText, true, nil, onDemandIP},
		{"anthropic.claude-3-haiku-20240307-v1:0", "Claude 3 Haiku", "Anthropic", mTextImage, mText, true, nil, onDemandIP},
		{"anthropic.claude-3-haiku-20240307-v1:0:200k", "Claude 3 Haiku", "Anthropic", mTextImage, mText, true, ft, provisioned},
		{"anthropic.claude-3-opus-20240229-v1:0", "Claude 3 Opus", "Anthropic", mTextImage, mText, true, nil, onDemandIP},
		{"anthropic.claude-3-5-sonnet-20240620-v1:0", "Claude 3.5 Sonnet", "Anthropic", mTextImage, mText, true, nil, onDemandIP},
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", "Claude 3.5 Sonnet v2", "Anthropic", mTextImage, mText, true, nil, profileOnly},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", "Claude 3.5 Haiku", "Anthropic", mText, mText, true, nil, profileOnly},
		{"anthropic.claude-3-7-sonnet-20250219-v1:0", "Claude 3.7 Sonnet", "Anthropic", mTextImage, mText, true, nil, profileOnly},
		{"anthropic.claude-sonnet-4-20250514-v1:0", "Claude Sonnet 4", "Anthropic", mTextImage, mText, true, nil, profileOnly},
		{"anthropic.claude-opus-4-20250514-v1:0", "Claude Opus 4", "Anthropic", mTextImage, mText, true, nil, profileOnly},

		{"amazon.titan-text-express-v1", "Titan Text G1 - Express", "Amazon", mText, mText, true, ftCPT, onDemand},
		{"amazon.titan-text-express-v1:0:8k", "Titan Text G1 - Express", "Amazon", mText, mText, true, ftCPT, provisioned},
		{"amazon.titan-text-lite-v1", "Titan Text G1 - Lite", "Amazon", mText, mText, true, ftCPT, onDemand},
		{"amazon.titan-text-premier-v1:0", "Titan Text G1 - Premier", "Amazon", mText, mText, true, nil, onDemand},
		{"amazon.titan-embed-text-v1", "Titan Embeddings G1 - Text", "Amazon", mText, mEmbed, false, nil, onDemand},
		{"amazon.titan-embed-text-v2:0", "Titan Text Embeddings V2", "Amazon", mText, mEmbed, false, nil, onDemand},
		{"amazon.titan-image-generator-v2:0", "Titan Image Generator G1 v2", "Amazon", mTextImage, mImage, false, ft, onDemand},
		{"amazon.nova-micro-v1:0", "Nova Micro", "Amazon", mText, mText, true, ftDistl, onDemandIP},
		{"amazon.nova-lite-v1:0", "Nova Lite", "Amazon", mTextImage, mText, true, ftDistl, onDemandIP},
		{"amazon.nova-pro-v1:0", "Nova Pro", "Amazon", mTextImage, mText, true, ftDistl, onDemandIP},
		{"amazon.nova-premier-v1:0", "Nova Premier", "Amazon", mTextImage, mText, true, nil, profileOnly},

		{"meta.llama3-8b-instruct-v1:0", "Llama 3 8B Instruct", "Meta", mText, mText, true, ft, onDemand},
		{"meta.llama3-70b-instruct-v1:0", "Llama 3 70B Instruct", "Meta", mText, mText, true, nil, onDemand},
		{"meta.llama3-1-8b-instruct-v1:0", "Llama 3.1 8B Instruct", "Meta", mText, mText, true, ft, onDemandIP},
		{"meta.llama3-1-70b-instruct-v1:0", "Llama 3.1 70B Instruct", "Meta", mText, mText, true, ft, onDemandIP},
		{"meta.llama3-2-1b-instruct-v1:0", "Llama 3.2 1B Instruct", "Meta", mText, mText, true, nil, profileOnly},
		{"meta.llama3-2-3b-instruct-v1:0", "Llama 3.2 3B Instruct", "Meta", mText, mText, true, nil, profileOnly},
		{"meta.llama3-3-70b-instruct-v1:0", "Llama 3.3 70B Instruct", "Meta", mText, mText, true, nil, profileOnly},

		{"cohere.command-text-v14", "Command", "Cohere", mText, mText, true, ft, onDemand},
		{"cohere.command-r-v1:0", "Command R", "Cohere", mText, mText, true, nil, onDemand},
		{"cohere.command-r-plus-v1:0", "Command R+", "Cohere", mText, mText, true, nil, onDemand},
		{"cohere.embed-english-v3", "Embed English", "Cohere", mText, mEmbed, false, nil, onDemand},
		{"cohere.embed-multilingual-v3", "Embed Multilingual", "Cohere", mText, mEmbed, false, nil, onDemand},

		{"mistral.mistral-7b-instruct-v0:2", "Mistral 7B Instruct", "Mistral AI", mText, mText, true, nil, onDemand},
		{"mistral.mixtral-8x7b-instruct-v0:1", "Mixtral 8x7B Instruct", "Mistral AI", mText, mText, true, nil, onDemand},
		{"mistral.mistral-large-2402-v1:0", "Mistral Large (24.02)", "Mistral AI", mText, mText, true, nil, onDemand},

		{"deepseek.r1-v1:0", "DeepSeek-R1", "DeepSeek", mText, mText, true, nil, profileOnly},
	}
}

// fmARN builds a foundation-model ARN (no account component, per AWS).
func fmARN(region, modelID string) string {
	return idgen.AWSARN("bedrock", region, "", "foundation-model/"+modelID)
}

// seedFoundationModels returns the foundation-model catalog for region.
func seedFoundationModels(region string) []driver.FoundationModel {
	specs := catalogSpecs()
	out := make([]driver.FoundationModel, 0, len(specs))

	for i := range specs {
		s := &specs[i]
		out = append(out, driver.FoundationModel{
			ModelARN:                   fmARN(region, s.id),
			ModelID:                    s.id,
			ModelName:                  s.name,
			ProviderName:               s.provider,
			InputModalities:            s.in,
			OutputModalities:           s.out,
			ResponseStreamingSupported: s.stream,
			CustomizationsSupported:    s.custom,
			InferenceTypesSupported:    s.inference,
			LifecycleStatus:            driver.LifecycleActive,
		})
	}

	return out
}

// geoGroup is a cross-region inference geography: the profile ID prefix, the
// display prefix and the regions it routes to.
type geoGroup struct {
	prefix, label string
	regions       []string
}

// geoFor returns the inference-profile geography that serves region.
func geoFor(region string) geoGroup {
	switch {
	case strings.HasPrefix(region, "eu-"):
		return geoGroup{"eu", "EU", []string{"eu-central-1", "eu-west-1", "eu-west-3"}}
	case strings.HasPrefix(region, "ap-"):
		return geoGroup{"apac", "APAC", []string{"ap-northeast-1", "ap-southeast-1", "ap-southeast-2"}}
	default:
		return geoGroup{"us", "US", []string{"us-east-1", "us-east-2", "us-west-2"}}
	}
}

// seedSystemProfiles builds one SYSTEM_DEFINED inference profile for every
// catalog model that supports INFERENCE_PROFILE. The first model ARN is the
// caller's own region, so resolution stays local.
func seedSystemProfiles(region, accountID, now string, catalog []driver.FoundationModel) []driver.InferenceProfile {
	geo := geoFor(region)
	regions := append([]string{region}, without(geo.regions, region)...)

	var out []driver.InferenceProfile

	for i := range catalog {
		fm := &catalog[i]
		if !contains(fm.InferenceTypesSupported, driver.InferenceTypeInferenceProfile) {
			continue
		}

		id := geo.prefix + "." + fm.ModelID
		models := make([]string, 0, len(regions))

		for _, r := range regions {
			models = append(models, fmARN(r, fm.ModelID))
		}

		out = append(out, driver.InferenceProfile{
			ARN:    idgen.AWSARN("bedrock", region, accountID, "inference-profile/"+id),
			ID:     id,
			Name:   geo.label + " " + fm.ProviderName + " " + fm.ModelName,
			Models: models,
			Status: driver.InferenceProfileStatusActive,
			Type:   driver.InferenceProfileTypeSystemDefined,
			Description: "Routes requests to " + fm.ProviderName + " " + fm.ModelName +
				" in " + strings.Join(regions, ", ") + ".",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	return out
}

// without returns list minus every occurrence of drop.
func without(list []string, drop string) []string {
	out := make([]string, 0, len(list))

	for _, v := range list {
		if v != drop {
			out = append(out, v)
		}
	}

	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}

	return false
}

// validateFoundationFilter rejects enum filter values AWS does not accept.
func validateFoundationFilter(f driver.FoundationModelFilter) error {
	checks := []struct {
		field, value string
		allowed      []string
	}{
		{"byCustomizationType", f.ByCustomizationType, []string{
			driver.CustomizationFineTuning, driver.CustomizationContinuedPreTraining, driver.CustomizationDistillation,
		}},
		{"byOutputModality", f.ByOutputModality, []string{driver.ModalityText, driver.ModalityImage, driver.ModalityEmbedding}},
		{"byInferenceType", f.ByInferenceType, []string{driver.InferenceTypeOnDemand, driver.InferenceTypeProvisioned}},
	}

	for _, c := range checks {
		if c.value != "" && !contains(c.allowed, c.value) {
			return errors.Newf(errors.InvalidArgument,
				"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
					"Member must satisfy enum value set: [%s]", c.value, c.field, strings.Join(c.allowed, ", "))
		}
	}

	return nil
}

// matchesFoundationFilter reports whether fm passes every set filter. The
// provider match ignores case; the enum filters are exact.
func matchesFoundationFilter(fm *driver.FoundationModel, f driver.FoundationModelFilter) bool {
	switch {
	case f.ByProvider != "" && !strings.EqualFold(fm.ProviderName, f.ByProvider):
		return false
	case f.ByCustomizationType != "" && !contains(fm.CustomizationsSupported, f.ByCustomizationType):
		return false
	case f.ByOutputModality != "" && !contains(fm.OutputModalities, f.ByOutputModality):
		return false
	case f.ByInferenceType != "" && !contains(fm.InferenceTypesSupported, f.ByInferenceType):
		return false
	default:
		return true
	}
}
