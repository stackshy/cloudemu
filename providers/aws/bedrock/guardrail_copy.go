package bedrock

import (
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// deepCopyGuardrailPolicies returns a deep copy of the given guardrail
// policies, allocating fresh pointers for each non-nil sub-policy and cloning
// every slice. Nil sub-policies are left nil. This severs any aliasing between
// a caller's config, the stored draft, and immutable version snapshots.
func deepCopyGuardrailPolicies(p driver.GuardrailPolicies) driver.GuardrailPolicies {
	var out driver.GuardrailPolicies

	if p.TopicPolicy != nil {
		out.TopicPolicy = &driver.GuardrailTopicPolicy{
			Topics: copyTopics(p.TopicPolicy.Topics),
		}
	}

	if p.ContentPolicy != nil {
		out.ContentPolicy = &driver.GuardrailContentPolicy{
			Filters: append([]driver.GuardrailContentFilter(nil), p.ContentPolicy.Filters...),
		}
	}

	if p.WordPolicy != nil {
		out.WordPolicy = &driver.GuardrailWordPolicy{
			Words:            append([]driver.GuardrailWord(nil), p.WordPolicy.Words...),
			ManagedWordLists: append([]driver.GuardrailManagedWordList(nil), p.WordPolicy.ManagedWordLists...),
		}
	}

	if p.SensitiveInformationPolicy != nil {
		out.SensitiveInformationPolicy = &driver.GuardrailSensitiveInformationPolicy{
			PiiEntities: append([]driver.GuardrailPiiEntity(nil), p.SensitiveInformationPolicy.PiiEntities...),
			Regexes:     append([]driver.GuardrailRegex(nil), p.SensitiveInformationPolicy.Regexes...),
		}
	}

	if p.ContextualGroundingPolicy != nil {
		out.ContextualGroundingPolicy = &driver.GuardrailContextualGroundingPolicy{
			Filters: append([]driver.GuardrailContextualGroundingFilter(nil), p.ContextualGroundingPolicy.Filters...),
		}
	}

	return out
}

// copyTopics clones a slice of topics, including each topic's Examples slice so
// the copy shares no backing array with the original.
func copyTopics(src []driver.GuardrailTopic) []driver.GuardrailTopic {
	if src == nil {
		return nil
	}

	out := make([]driver.GuardrailTopic, len(src))

	for i, t := range src {
		t.Examples = append([]string(nil), t.Examples...)
		out[i] = t
	}

	return out
}
