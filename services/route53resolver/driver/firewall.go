package driver

import "context"

// ---- DNS Firewall ----------------------------------------------------------

// FirewallDomainList is a reusable set of domains a firewall rule can act on.
type FirewallDomainList struct {
	ID               string
	ARN              string
	Name             string
	CreatorRequestID string
	Category         string
	ManagedListType  string
	ManagedOwnerName string
	DomainCount      int32
	Status           string
	StatusMessage    string
	CreatedAt        string
	ModifiedAt       string
}

// FirewallRule links a domain list to an action inside a rule group. Within a
// group a rule is identified by (FirewallDomainListID, Qtype).
type FirewallRule struct {
	FirewallRuleGroupID             string
	FirewallDomainListID            string
	Name                            string
	Priority                        int32
	Action                          string
	BlockResponse                   string
	BlockOverrideDomain             string
	BlockOverrideDNSType            string
	BlockOverrideTTL                int32
	Qtype                           string
	ConfidenceThreshold             string
	DNSThreatProtection             string
	FirewallDomainRedirectionAction string
	CreatorRequestID                string
	Status                          string
	StatusMessage                   string
	CreatedAt                       string
	ModifiedAt                      string
}

// FirewallRuleGroup is a named, ordered container of firewall rules.
type FirewallRuleGroup struct {
	ID               string
	ARN              string
	Name             string
	CreatorRequestID string
	OwnerID          string
	RuleCount        int32
	ShareStatus      string
	Status           string
	StatusMessage    string
	CreatedAt        string
	ModifiedAt       string
}

// FirewallRuleGroupAssociation binds a rule group to a VPC at a priority.
type FirewallRuleGroupAssociation struct {
	ID                  string
	ARN                 string
	Name                string
	CreatorRequestID    string
	FirewallRuleGroupID string
	VPCID               string
	Priority            int32
	MutationProtection  string
	ManagedOwnerName    string
	Status              string
	StatusMessage       string
	CreatedAt           string
	ModifiedAt          string
}

// FirewallConfig is the per-VPC firewall fail-open behavior.
type FirewallConfig struct {
	ID               string
	OwnerID          string
	ResourceID       string
	FirewallFailOpen string
}

// FirewallRuleInput carries mutable firewall-rule fields (create and update).
type FirewallRuleInput struct {
	FirewallRuleGroupID             string
	FirewallDomainListID            string
	Name                            string
	Priority                        int32
	Action                          string
	BlockResponse                   string
	BlockOverrideDomain             string
	BlockOverrideDNSType            string
	BlockOverrideTTL                int32
	Qtype                           string
	ConfidenceThreshold             string
	DNSThreatProtection             string
	FirewallDomainRedirectionAction string
	CreatorRequestID                string
}

// FirewallService is the DNS Firewall resource group.
type FirewallService interface {
	// Domain lists
	CreateFirewallDomainList(ctx context.Context, creatorRequestID, name string, tags []Tag) (*FirewallDomainList, error)
	GetFirewallDomainList(ctx context.Context, id string) (*FirewallDomainList, error)
	DeleteFirewallDomainList(ctx context.Context, id string) (*FirewallDomainList, error)
	ListFirewallDomainLists(ctx context.Context) ([]FirewallDomainList, error)
	UpdateFirewallDomains(ctx context.Context, id, operation string, domains []string) (*FirewallDomainList, error)
	ImportFirewallDomains(ctx context.Context, id, operation, domainFileURL string) (*FirewallDomainList, error)
	ListFirewallDomains(ctx context.Context, id string) ([]string, error)

	// Rules
	CreateFirewallRule(ctx context.Context, in *FirewallRuleInput) (*FirewallRule, error)
	UpdateFirewallRule(ctx context.Context, in *FirewallRuleInput) (*FirewallRule, error)
	DeleteFirewallRule(ctx context.Context, groupID, domainListID, qtype string) (*FirewallRule, error)
	ListFirewallRules(ctx context.Context, groupID string) ([]FirewallRule, error)
	BatchCreateFirewallRules(ctx context.Context, in []FirewallRuleInput) ([]FirewallRule, error)
	BatchUpdateFirewallRules(ctx context.Context, in []FirewallRuleInput) ([]FirewallRule, error)
	BatchDeleteFirewallRules(ctx context.Context, groupID string, keys []FirewallRuleKey) ([]FirewallRule, error)

	// Rule groups
	CreateFirewallRuleGroup(ctx context.Context, creatorRequestID, name string, tags []Tag) (*FirewallRuleGroup, error)
	GetFirewallRuleGroup(ctx context.Context, id string) (*FirewallRuleGroup, error)
	DeleteFirewallRuleGroup(ctx context.Context, id string) (*FirewallRuleGroup, error)
	ListFirewallRuleGroups(ctx context.Context) ([]FirewallRuleGroup, error)
	PutFirewallRuleGroupPolicy(ctx context.Context, arn, policy string) error
	GetFirewallRuleGroupPolicy(ctx context.Context, arn string) (string, error)

	// Rule-group associations
	AssociateFirewallRuleGroup(ctx context.Context, in *AssociateFirewallRuleGroupInput) (*FirewallRuleGroupAssociation, error)
	DisassociateFirewallRuleGroup(ctx context.Context, assocID string) (*FirewallRuleGroupAssociation, error)
	GetFirewallRuleGroupAssociation(ctx context.Context, assocID string) (*FirewallRuleGroupAssociation, error)
	ListFirewallRuleGroupAssociations(ctx context.Context) ([]FirewallRuleGroupAssociation, error)
	UpdateFirewallRuleGroupAssociation(ctx context.Context, in *UpdateFirewallRuleGroupAssociationInput) (*FirewallRuleGroupAssociation, error)

	// Firewall configs
	GetFirewallConfig(ctx context.Context, resourceID string) (*FirewallConfig, error)
	UpdateFirewallConfig(ctx context.Context, resourceID, failOpen string) (*FirewallConfig, error)
	ListFirewallConfigs(ctx context.Context) ([]FirewallConfig, error)

	// Rule-type enumeration (static descriptor list; empty in the mock).
	ListFirewallRuleTypes(ctx context.Context) ([]FirewallRuleType, error)
}

// FirewallRuleKey identifies a rule within a group for batch delete.
type FirewallRuleKey struct {
	FirewallDomainListID string
	Qtype                string
}

// AssociateFirewallRuleGroupInput carries the associate request fields.
type AssociateFirewallRuleGroupInput struct {
	CreatorRequestID    string
	FirewallRuleGroupID string
	Name                string
	Priority            int32
	VPCID               string
	MutationProtection  string
	Tags                []Tag
}

// UpdateFirewallRuleGroupAssociationInput carries the mutable association fields.
type UpdateFirewallRuleGroupAssociationInput struct {
	ID                 string
	MutationProtection string
	Name               string
	Priority           int32
}

// FirewallRuleType is one entry of the rule-type descriptor enumeration.
type FirewallRuleType struct {
	Name string
}
