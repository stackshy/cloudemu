// Package driver defines the interface and types for AWS WAFv2
// implementations. It models WebACLs, IPSets, RuleGroups and RegexPatternSets,
// their optimistic-lock tokens, tags, and web-ACL/resource associations.
//
// WAFv2 partitions its namespace by Scope (REGIONAL vs CLOUDFRONT): a resource
// is identified by the tuple (scope, id). Every resource carries a LockToken
// that changes on each mutation; Update and Delete must present the current
// token or the backend returns a WAFOptimisticLockException.
package driver

import (
	"context"
	"encoding/json"
)

// Scope values partition the WAFv2 namespace.
const (
	ScopeRegional   = "REGIONAL"
	ScopeCloudFront = "CLOUDFRONT"
)

// WebACL is the stored form of a web ACL. Rule, DefaultAction, VisibilityConfig
// and other nested configuration blocks are stored verbatim as raw JSON so Get
// returns exactly what Create/Update put.
type WebACL struct {
	ID               string
	Name             string
	ARN              string
	Scope            string
	Description      string
	LockToken        string
	Capacity         int64
	LabelNamespace   string
	DefaultAction    json.RawMessage
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	TokenDomains     json.RawMessage
	CustomResponses  json.RawMessage
	CaptchaConfig    json.RawMessage
	ChallengeConfig  json.RawMessage
	Tags             map[string]string
}

// IPSet is the stored form of an IP set.
type IPSet struct {
	ID               string
	Name             string
	ARN              string
	Scope            string
	Description      string
	LockToken        string
	IPAddressVersion string
	Addresses        []string
	Tags             map[string]string
}

// RuleGroup is the stored form of a rule group.
type RuleGroup struct {
	ID               string
	Name             string
	ARN              string
	Scope            string
	Description      string
	LockToken        string
	Capacity         int64
	LabelNamespace   string
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	CustomResponses  json.RawMessage
	Tags             map[string]string
}

// RegexPatternSet is the stored form of a regex pattern set.
type RegexPatternSet struct {
	ID                    string
	Name                  string
	ARN                   string
	Scope                 string
	Description           string
	LockToken             string
	RegularExpressionList json.RawMessage
	Tags                  map[string]string
}

// CreateWebACLInput describes a web ACL to create.
type CreateWebACLInput struct {
	Name             string
	Scope            string
	Description      string
	DefaultAction    json.RawMessage
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	TokenDomains     json.RawMessage
	CustomResponses  json.RawMessage
	CaptchaConfig    json.RawMessage
	ChallengeConfig  json.RawMessage
	Tags             map[string]string
}

// UpdateWebACLInput describes an update to an existing web ACL.
type UpdateWebACLInput struct {
	Name             string
	Scope            string
	ID               string
	LockToken        string
	Description      string
	DefaultAction    json.RawMessage
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	TokenDomains     json.RawMessage
	CustomResponses  json.RawMessage
	CaptchaConfig    json.RawMessage
	ChallengeConfig  json.RawMessage
}

// Ref identifies a resource within a scope.
type Ref struct {
	Scope string
	ID    string
	Name  string
}

// CreateIPSetInput describes an IP set to create.
type CreateIPSetInput struct {
	Name             string
	Scope            string
	Description      string
	IPAddressVersion string
	Addresses        []string
	Tags             map[string]string
}

// UpdateIPSetInput describes an update to an existing IP set.
type UpdateIPSetInput struct {
	Name        string
	Scope       string
	ID          string
	LockToken   string
	Description string
	Addresses   []string
}

// CreateRuleGroupInput describes a rule group to create.
type CreateRuleGroupInput struct {
	Name             string
	Scope            string
	Description      string
	Capacity         int64
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	CustomResponses  json.RawMessage
	Tags             map[string]string
}

// UpdateRuleGroupInput describes an update to an existing rule group.
type UpdateRuleGroupInput struct {
	Name             string
	Scope            string
	ID               string
	LockToken        string
	Description      string
	VisibilityConfig json.RawMessage
	Rules            json.RawMessage
	CustomResponses  json.RawMessage
}

// CreateRegexPatternSetInput describes a regex pattern set to create.
type CreateRegexPatternSetInput struct {
	Name                  string
	Scope                 string
	Description           string
	RegularExpressionList json.RawMessage
	Tags                  map[string]string
}

// UpdateRegexPatternSetInput describes an update to an existing regex pattern set.
type UpdateRegexPatternSetInput struct {
	Name                  string
	Scope                 string
	ID                    string
	LockToken             string
	Description           string
	RegularExpressionList json.RawMessage
}

// WAFV2 is the interface a WAFv2 backend implements.
type WAFV2 interface {
	CreateWebACL(ctx context.Context, in CreateWebACLInput) (*WebACL, error)
	GetWebACL(ctx context.Context, ref Ref) (*WebACL, error)
	UpdateWebACL(ctx context.Context, in UpdateWebACLInput) (newLockToken string, err error)
	DeleteWebACL(ctx context.Context, ref Ref, lockToken string) error
	ListWebACLs(ctx context.Context, scope string) ([]WebACL, error)

	CreateIPSet(ctx context.Context, in CreateIPSetInput) (*IPSet, error)
	GetIPSet(ctx context.Context, ref Ref) (*IPSet, error)
	UpdateIPSet(ctx context.Context, in UpdateIPSetInput) (newLockToken string, err error)
	DeleteIPSet(ctx context.Context, ref Ref, lockToken string) error
	ListIPSets(ctx context.Context, scope string) ([]IPSet, error)

	CreateRuleGroup(ctx context.Context, in CreateRuleGroupInput) (*RuleGroup, error)
	GetRuleGroup(ctx context.Context, ref Ref) (*RuleGroup, error)
	UpdateRuleGroup(ctx context.Context, in UpdateRuleGroupInput) (newLockToken string, err error)
	DeleteRuleGroup(ctx context.Context, ref Ref, lockToken string) error
	ListRuleGroups(ctx context.Context, scope string) ([]RuleGroup, error)

	CreateRegexPatternSet(ctx context.Context, in CreateRegexPatternSetInput) (*RegexPatternSet, error)
	GetRegexPatternSet(ctx context.Context, ref Ref) (*RegexPatternSet, error)
	UpdateRegexPatternSet(ctx context.Context, in UpdateRegexPatternSetInput) (newLockToken string, err error)
	DeleteRegexPatternSet(ctx context.Context, ref Ref, lockToken string) error
	ListRegexPatternSets(ctx context.Context, scope string) ([]RegexPatternSet, error)

	AssociateWebACL(ctx context.Context, webACLARN, resourceARN string) error
	DisassociateWebACL(ctx context.Context, resourceARN string) error
	GetWebACLForResource(ctx context.Context, resourceARN string) (*WebACL, error)
	ListResourcesForWebACL(ctx context.Context, webACLARN, resourceType string) ([]string, error)

	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string) (resourceARN string, tags map[string]string, err error)
}
