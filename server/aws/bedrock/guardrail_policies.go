package bedrock

import bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"

// --- leaf wire types (shared between request and response: identical keys) ---

type topicJSON struct {
	Name       string   `json:"name"`
	Definition string   `json:"definition"`
	Examples   []string `json:"examples,omitempty"`
	Type       string   `json:"type,omitempty"`
}

type contentFilterJSON struct {
	Type           string `json:"type"`
	InputStrength  string `json:"inputStrength"`
	OutputStrength string `json:"outputStrength"`
}

type wordJSON struct {
	Text string `json:"text"`
}

type managedWordsJSON struct {
	Type string `json:"type"`
}

type piiEntityJSON struct {
	Type   string `json:"type"`
	Action string `json:"action"`
}

type regexJSON struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Action      string `json:"action"`
	Description string `json:"description,omitempty"`
}

type contextualGroundingFilterJSON struct {
	Type      string  `json:"type"`
	Threshold float64 `json:"threshold"`
	Action    string  `json:"action,omitempty"`
}

// --- request wrappers ("...Config"-suffixed keys) ---

type topicPolicyConfigJSON struct {
	TopicsConfig []topicJSON `json:"topicsConfig"`
}

type contentPolicyConfigJSON struct {
	FiltersConfig []contentFilterJSON `json:"filtersConfig"`
}

type wordPolicyConfigJSON struct {
	WordsConfig            []wordJSON         `json:"wordsConfig,omitempty"`
	ManagedWordListsConfig []managedWordsJSON `json:"managedWordListsConfig,omitempty"`
}

type sensitiveInfoPolicyConfigJSON struct {
	PiiEntitiesConfig []piiEntityJSON `json:"piiEntitiesConfig,omitempty"`
	RegexesConfig     []regexJSON     `json:"regexesConfig,omitempty"`
}

type contextualGroundingPolicyConfigJSON struct {
	FiltersConfig []contextualGroundingFilterJSON `json:"filtersConfig"`
}

// --- response wrappers ("Config" suffix dropped) ---

type topicPolicyJSON struct {
	Topics []topicJSON `json:"topics"`
}

type contentPolicyJSON struct {
	Filters []contentFilterJSON `json:"filters"`
}

type wordPolicyJSON struct {
	Words            []wordJSON         `json:"words,omitempty"`
	ManagedWordLists []managedWordsJSON `json:"managedWordLists,omitempty"`
}

type sensitiveInfoPolicyJSON struct {
	PiiEntities []piiEntityJSON `json:"piiEntities,omitempty"`
	Regexes     []regexJSON     `json:"regexes,omitempty"`
}

type contextualGroundingPolicyJSON struct {
	Filters []contextualGroundingFilterJSON `json:"filters"`
}

// --- request -> driver conversion ---

func toDriverGuardrailPolicies(in *createGuardrailRequest) bedrockdriver.GuardrailPolicies {
	var p bedrockdriver.GuardrailPolicies

	if c := in.TopicPolicyConfig; c != nil {
		p.TopicPolicy = &bedrockdriver.GuardrailTopicPolicy{Topics: toDriverTopics(c.TopicsConfig)}
	}

	if c := in.ContentPolicyConfig; c != nil {
		p.ContentPolicy = &bedrockdriver.GuardrailContentPolicy{Filters: toDriverContentFilters(c.FiltersConfig)}
	}

	if c := in.WordPolicyConfig; c != nil {
		p.WordPolicy = &bedrockdriver.GuardrailWordPolicy{
			Words:            toDriverWords(c.WordsConfig),
			ManagedWordLists: toDriverManagedWords(c.ManagedWordListsConfig),
		}
	}

	if c := in.SensitiveInformationPolicyConfig; c != nil {
		p.SensitiveInformationPolicy = &bedrockdriver.GuardrailSensitiveInformationPolicy{
			PiiEntities: toDriverPiiEntities(c.PiiEntitiesConfig),
			Regexes:     toDriverRegexes(c.RegexesConfig),
		}
	}

	if c := in.ContextualGroundingPolicyConfig; c != nil {
		p.ContextualGroundingPolicy = &bedrockdriver.GuardrailContextualGroundingPolicy{
			Filters: toDriverGroundingFilters(c.FiltersConfig),
		}
	}

	return p
}

func toDriverTopics(in []topicJSON) []bedrockdriver.GuardrailTopic {
	out := make([]bedrockdriver.GuardrailTopic, len(in))
	for i, t := range in {
		out[i] = bedrockdriver.GuardrailTopic{Name: t.Name, Definition: t.Definition, Examples: t.Examples, Type: t.Type}
	}

	return out
}

func toDriverContentFilters(in []contentFilterJSON) []bedrockdriver.GuardrailContentFilter {
	out := make([]bedrockdriver.GuardrailContentFilter, len(in))
	for i, f := range in {
		out[i] = bedrockdriver.GuardrailContentFilter{Type: f.Type, InputStrength: f.InputStrength, OutputStrength: f.OutputStrength}
	}

	return out
}

func toDriverWords(in []wordJSON) []bedrockdriver.GuardrailWord {
	out := make([]bedrockdriver.GuardrailWord, len(in))
	for i, w := range in {
		out[i] = bedrockdriver.GuardrailWord{Text: w.Text}
	}

	return out
}

func toDriverManagedWords(in []managedWordsJSON) []bedrockdriver.GuardrailManagedWordList {
	out := make([]bedrockdriver.GuardrailManagedWordList, len(in))
	for i, w := range in {
		out[i] = bedrockdriver.GuardrailManagedWordList{Type: w.Type}
	}

	return out
}

func toDriverPiiEntities(in []piiEntityJSON) []bedrockdriver.GuardrailPiiEntity {
	out := make([]bedrockdriver.GuardrailPiiEntity, len(in))
	for i, e := range in {
		out[i] = bedrockdriver.GuardrailPiiEntity{Type: e.Type, Action: e.Action}
	}

	return out
}

func toDriverRegexes(in []regexJSON) []bedrockdriver.GuardrailRegex {
	out := make([]bedrockdriver.GuardrailRegex, len(in))
	for i, r := range in {
		out[i] = bedrockdriver.GuardrailRegex{Name: r.Name, Pattern: r.Pattern, Action: r.Action, Description: r.Description}
	}

	return out
}

func toDriverGroundingFilters(in []contextualGroundingFilterJSON) []bedrockdriver.GuardrailContextualGroundingFilter {
	out := make([]bedrockdriver.GuardrailContextualGroundingFilter, len(in))
	for i, f := range in {
		out[i] = bedrockdriver.GuardrailContextualGroundingFilter{Type: f.Type, Threshold: f.Threshold, Action: f.Action}
	}

	return out
}

// --- driver -> response conversion ---

func fillGuardrailPolicies(out *guardrailJSON, p *bedrockdriver.GuardrailPolicies) {
	if tp := p.TopicPolicy; tp != nil {
		out.TopicPolicy = &topicPolicyJSON{Topics: fromDriverTopics(tp.Topics)}
	}

	if cp := p.ContentPolicy; cp != nil {
		out.ContentPolicy = &contentPolicyJSON{Filters: fromDriverContentFilters(cp.Filters)}
	}

	if wp := p.WordPolicy; wp != nil {
		out.WordPolicy = &wordPolicyJSON{
			Words:            fromDriverWords(wp.Words),
			ManagedWordLists: fromDriverManagedWords(wp.ManagedWordLists),
		}
	}

	if sp := p.SensitiveInformationPolicy; sp != nil {
		out.SensitiveInformationPolicy = &sensitiveInfoPolicyJSON{
			PiiEntities: fromDriverPiiEntities(sp.PiiEntities),
			Regexes:     fromDriverRegexes(sp.Regexes),
		}
	}

	if gp := p.ContextualGroundingPolicy; gp != nil {
		out.ContextualGroundingPolicy = &contextualGroundingPolicyJSON{Filters: fromDriverGroundingFilters(gp.Filters)}
	}
}

func fromDriverTopics(in []bedrockdriver.GuardrailTopic) []topicJSON {
	out := make([]topicJSON, len(in))
	for i, t := range in {
		out[i] = topicJSON{Name: t.Name, Definition: t.Definition, Examples: t.Examples, Type: t.Type}
	}

	return out
}

func fromDriverContentFilters(in []bedrockdriver.GuardrailContentFilter) []contentFilterJSON {
	out := make([]contentFilterJSON, len(in))
	for i, f := range in {
		out[i] = contentFilterJSON{Type: f.Type, InputStrength: f.InputStrength, OutputStrength: f.OutputStrength}
	}

	return out
}

func fromDriverWords(in []bedrockdriver.GuardrailWord) []wordJSON {
	out := make([]wordJSON, len(in))
	for i, w := range in {
		out[i] = wordJSON{Text: w.Text}
	}

	return out
}

func fromDriverManagedWords(in []bedrockdriver.GuardrailManagedWordList) []managedWordsJSON {
	out := make([]managedWordsJSON, len(in))
	for i, w := range in {
		out[i] = managedWordsJSON{Type: w.Type}
	}

	return out
}

func fromDriverPiiEntities(in []bedrockdriver.GuardrailPiiEntity) []piiEntityJSON {
	out := make([]piiEntityJSON, len(in))
	for i, e := range in {
		out[i] = piiEntityJSON{Type: e.Type, Action: e.Action}
	}

	return out
}

func fromDriverRegexes(in []bedrockdriver.GuardrailRegex) []regexJSON {
	out := make([]regexJSON, len(in))
	for i, r := range in {
		out[i] = regexJSON{Name: r.Name, Pattern: r.Pattern, Action: r.Action, Description: r.Description}
	}

	return out
}

func fromDriverGroundingFilters(in []bedrockdriver.GuardrailContextualGroundingFilter) []contextualGroundingFilterJSON {
	out := make([]contextualGroundingFilterJSON, len(in))
	for i, f := range in {
		out[i] = contextualGroundingFilterJSON{Type: f.Type, Threshold: f.Threshold, Action: f.Action}
	}

	return out
}
