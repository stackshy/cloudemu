package wafv2

import (
	"encoding/json"

	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToMap(tags []tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// --- WebACL wire shapes ---

// webACLJSON is the wire shape of a WAFv2 WebACL. Nested rule/statement blocks
// are passed through as raw JSON so what the client sends round-trips verbatim.
type webACLJSON struct {
	Name                 string          `json:"Name"`
	ID                   string          `json:"Id"`
	ARN                  string          `json:"ARN"`
	Description          string          `json:"Description,omitempty"`
	Capacity             int64           `json:"Capacity"`
	LabelNamespace       string          `json:"LabelNamespace,omitempty"`
	DefaultAction        json.RawMessage `json:"DefaultAction,omitempty"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig,omitempty"`
	Rules                json.RawMessage `json:"Rules,omitempty"`
	TokenDomains         json.RawMessage `json:"TokenDomains,omitempty"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies,omitempty"`
	CaptchaConfig        json.RawMessage `json:"CaptchaConfig,omitempty"`
	ChallengeConfig      json.RawMessage `json:"ChallengeConfig,omitempty"`
}

func webACLToWire(a *wafdriver.WebACL) webACLJSON {
	return webACLJSON{
		Name: a.Name, ID: a.ID, ARN: a.ARN, Description: a.Description,
		Capacity: a.Capacity, LabelNamespace: a.LabelNamespace,
		DefaultAction: a.DefaultAction, VisibilityConfig: a.VisibilityConfig,
		Rules: a.Rules, TokenDomains: a.TokenDomains,
		CustomResponseBodies: a.CustomResponses,
		CaptchaConfig:        a.CaptchaConfig, ChallengeConfig: a.ChallengeConfig,
	}
}

type summaryJSON struct {
	Name        string `json:"Name"`
	ID          string `json:"Id"`
	ARN         string `json:"ARN"`
	Description string `json:"Description,omitempty"`
	LockToken   string `json:"LockToken,omitempty"`
}

func webACLSummary(a *wafdriver.WebACL) summaryJSON {
	return summaryJSON{Name: a.Name, ID: a.ID, ARN: a.ARN, Description: a.Description, LockToken: a.LockToken}
}

// --- IPSet wire shapes ---

type ipSetJSON struct {
	Name             string   `json:"Name"`
	ID               string   `json:"Id"`
	ARN              string   `json:"ARN"`
	Description      string   `json:"Description,omitempty"`
	IPAddressVersion string   `json:"IPAddressVersion"`
	Addresses        []string `json:"Addresses"`
}

func ipSetToWire(s *wafdriver.IPSet) ipSetJSON {
	addrs := s.Addresses
	if addrs == nil {
		addrs = []string{}
	}

	return ipSetJSON{
		Name: s.Name, ID: s.ID, ARN: s.ARN, Description: s.Description,
		IPAddressVersion: s.IPAddressVersion, Addresses: addrs,
	}
}

func ipSetSummary(s *wafdriver.IPSet) summaryJSON {
	return summaryJSON{Name: s.Name, ID: s.ID, ARN: s.ARN, Description: s.Description, LockToken: s.LockToken}
}

// --- RuleGroup wire shapes ---

type ruleGroupJSON struct {
	Name                 string          `json:"Name"`
	ID                   string          `json:"Id"`
	ARN                  string          `json:"ARN"`
	Description          string          `json:"Description,omitempty"`
	Capacity             int64           `json:"Capacity"`
	LabelNamespace       string          `json:"LabelNamespace,omitempty"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig,omitempty"`
	Rules                json.RawMessage `json:"Rules,omitempty"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies,omitempty"`
}

func ruleGroupToWire(g *wafdriver.RuleGroup) ruleGroupJSON {
	return ruleGroupJSON{
		Name: g.Name, ID: g.ID, ARN: g.ARN, Description: g.Description,
		Capacity: g.Capacity, LabelNamespace: g.LabelNamespace,
		VisibilityConfig: g.VisibilityConfig, Rules: g.Rules,
		CustomResponseBodies: g.CustomResponses,
	}
}

func ruleGroupSummary(g *wafdriver.RuleGroup) summaryJSON {
	return summaryJSON{Name: g.Name, ID: g.ID, ARN: g.ARN, Description: g.Description, LockToken: g.LockToken}
}

// --- RegexPatternSet wire shapes ---

type regexSetJSON struct {
	Name                  string          `json:"Name"`
	ID                    string          `json:"Id"`
	ARN                   string          `json:"ARN"`
	Description           string          `json:"Description,omitempty"`
	RegularExpressionList json.RawMessage `json:"RegularExpressionList,omitempty"`
}

func regexSetToWire(s *wafdriver.RegexPatternSet) regexSetJSON {
	return regexSetJSON{
		Name: s.Name, ID: s.ID, ARN: s.ARN, Description: s.Description,
		RegularExpressionList: s.RegularExpressionList,
	}
}

func regexSetSummary(s *wafdriver.RegexPatternSet) summaryJSON {
	return summaryJSON{Name: s.Name, ID: s.ID, ARN: s.ARN, Description: s.Description, LockToken: s.LockToken}
}

// --- request shapes ---

type createWebACLRequest struct {
	Name                 string          `json:"Name"`
	Scope                string          `json:"Scope"`
	Description          string          `json:"Description"`
	DefaultAction        json.RawMessage `json:"DefaultAction"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig"`
	Rules                json.RawMessage `json:"Rules"`
	TokenDomains         json.RawMessage `json:"TokenDomains"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies"`
	CaptchaConfig        json.RawMessage `json:"CaptchaConfig"`
	ChallengeConfig      json.RawMessage `json:"ChallengeConfig"`
	Tags                 []tag           `json:"Tags"`
}

type updateWebACLRequest struct {
	Name                 string          `json:"Name"`
	Scope                string          `json:"Scope"`
	ID                   string          `json:"Id"`
	LockToken            string          `json:"LockToken"`
	Description          string          `json:"Description"`
	DefaultAction        json.RawMessage `json:"DefaultAction"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig"`
	Rules                json.RawMessage `json:"Rules"`
	TokenDomains         json.RawMessage `json:"TokenDomains"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies"`
	CaptchaConfig        json.RawMessage `json:"CaptchaConfig"`
	ChallengeConfig      json.RawMessage `json:"ChallengeConfig"`
}

type refRequest struct {
	Name      string `json:"Name"`
	Scope     string `json:"Scope"`
	ID        string `json:"Id"`
	LockToken string `json:"LockToken"`
}

type listRequest struct {
	Scope string `json:"Scope"`
}

type createIPSetRequest struct {
	Name             string   `json:"Name"`
	Scope            string   `json:"Scope"`
	Description      string   `json:"Description"`
	IPAddressVersion string   `json:"IPAddressVersion"`
	Addresses        []string `json:"Addresses"`
	Tags             []tag    `json:"Tags"`
}

type updateIPSetRequest struct {
	Name        string   `json:"Name"`
	Scope       string   `json:"Scope"`
	ID          string   `json:"Id"`
	LockToken   string   `json:"LockToken"`
	Description string   `json:"Description"`
	Addresses   []string `json:"Addresses"`
}

type createRuleGroupRequest struct {
	Name                 string          `json:"Name"`
	Scope                string          `json:"Scope"`
	Description          string          `json:"Description"`
	Capacity             int64           `json:"Capacity"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig"`
	Rules                json.RawMessage `json:"Rules"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies"`
	Tags                 []tag           `json:"Tags"`
}

type updateRuleGroupRequest struct {
	Name                 string          `json:"Name"`
	Scope                string          `json:"Scope"`
	ID                   string          `json:"Id"`
	LockToken            string          `json:"LockToken"`
	Description          string          `json:"Description"`
	VisibilityConfig     json.RawMessage `json:"VisibilityConfig"`
	Rules                json.RawMessage `json:"Rules"`
	CustomResponseBodies json.RawMessage `json:"CustomResponseBodies"`
}

type createRegexSetRequest struct {
	Name                  string          `json:"Name"`
	Scope                 string          `json:"Scope"`
	Description           string          `json:"Description"`
	RegularExpressionList json.RawMessage `json:"RegularExpressionList"`
	Tags                  []tag           `json:"Tags"`
}

type updateRegexSetRequest struct {
	Name                  string          `json:"Name"`
	Scope                 string          `json:"Scope"`
	ID                    string          `json:"Id"`
	LockToken             string          `json:"LockToken"`
	Description           string          `json:"Description"`
	RegularExpressionList json.RawMessage `json:"RegularExpressionList"`
}

type associateRequest struct {
	WebACLArn   string `json:"WebACLArn"`
	ResourceArn string `json:"ResourceArn"`
}

type disassociateRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type getForResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type listResourcesRequest struct {
	WebACLArn    string `json:"WebACLArn"`
	ResourceType string `json:"ResourceType"`
}

type tagResourceRequest struct {
	ResourceARN string `json:"ResourceARN"`
	Tags        []tag  `json:"Tags"`
}

type untagResourceRequest struct {
	ResourceARN string   `json:"ResourceARN"`
	TagKeys     []string `json:"TagKeys"`
}

type listTagsRequest struct {
	ResourceARN string `json:"ResourceARN"`
}

// --- response shapes ---

type createSummaryResponse struct {
	Summary summaryJSON `json:"Summary"`
}

type getWebACLResponse struct {
	WebACL    webACLJSON `json:"WebACL"`
	LockToken string     `json:"LockToken"`
}

type getIPSetResponse struct {
	IPSet     ipSetJSON `json:"IPSet"`
	LockToken string    `json:"LockToken"`
}

type getRuleGroupResponse struct {
	RuleGroup ruleGroupJSON `json:"RuleGroup"`
	LockToken string        `json:"LockToken"`
}

type getRegexSetResponse struct {
	RegexPatternSet regexSetJSON `json:"RegexPatternSet"`
	LockToken       string       `json:"LockToken"`
}

type lockTokenResponse struct {
	NextLockToken string `json:"NextLockToken"`
}

type listWebACLsResponse struct {
	WebACLs    []summaryJSON `json:"WebACLs"`
	NextMarker string        `json:"NextMarker,omitempty"`
}

type listIPSetsResponse struct {
	IPSets     []summaryJSON `json:"IPSets"`
	NextMarker string        `json:"NextMarker,omitempty"`
}

type listRuleGroupsResponse struct {
	RuleGroups []summaryJSON `json:"RuleGroups"`
	NextMarker string        `json:"NextMarker,omitempty"`
}

type listRegexSetsResponse struct {
	RegexPatternSets []summaryJSON `json:"RegexPatternSets"`
	NextMarker       string        `json:"NextMarker,omitempty"`
}

type getForResourceResponse struct {
	WebACL *webACLJSON `json:"WebACL,omitempty"`
}

type listResourcesResponse struct {
	ResourceArns []string `json:"ResourceArns"`
}

type listTagsResponse struct {
	TagInfoForResource tagInfoJSON `json:"TagInfoForResource"`
	NextMarker         string      `json:"NextMarker,omitempty"`
}

type tagInfoJSON struct {
	ResourceARN string `json:"ResourceARN"`
	TagList     []tag  `json:"TagList"`
}
