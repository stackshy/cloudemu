// Package driver defines the interface and types for the AWS Backup
// control-plane API: backup vaults (with their access policy, event
// notifications and Vault Lock configuration), backup plans (with rule bodies
// and version history) and backup selections that assign resources to a plan.
//
// The emulator is control-plane only: there is NO backup-job / recovery-point
// data plane. Every computed field — the vault and plan ARNs, the plan id, a
// per-version VersionId, the selection id and all creation timestamps — is
// minted once at create and stored, so repeated Describe/Get/List reads never
// drift. Rule, lifecycle, copy-action and selection-condition blocks round-trip
// verbatim. Vault Lock is modeled as a state machine: a lock set with
// ChangeableForDays (COMPLIANCE mode) records a future LockDate and becomes
// immutable once that date passes; a lock set without one (GOVERNANCE mode) can
// be changed or removed at any time.
package driver

import (
	"context"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Lifecycle is a backup rule or copy-action lifecycle. The block round-trips
// verbatim; the pointer fields let an explicit 0/false survive a round-trip and
// an absent member read back as null.
type Lifecycle struct {
	MoveToColdStorageAfterDays          *int64 `json:"moveToColdStorageAfterDays,omitempty"`
	DeleteAfterDays                     *int64 `json:"deleteAfterDays,omitempty"`
	OptInToArchiveForSupportedResources *bool  `json:"optInToArchiveForSupportedResources,omitempty"`
}

// CopyAction copies a recovery point to a destination vault under an optional
// lifecycle. The block round-trips verbatim.
type CopyAction struct {
	DestinationBackupVaultArn string     `json:"destinationBackupVaultArn,omitempty"`
	Lifecycle                 *Lifecycle `json:"lifecycle,omitempty"`
}

// Rule is one backup rule in a plan. RuleID is computed once and stable; the
// remaining members round-trip verbatim.
type Rule struct {
	RuleName                string            `json:"ruleName,omitempty"`
	TargetBackupVaultName   string            `json:"targetBackupVaultName,omitempty"`
	ScheduleExpression      string            `json:"scheduleExpression,omitempty"`
	ScheduleExpressionTZ    string            `json:"scheduleExpressionTimezone,omitempty"`
	StartWindowMinutes      *int64            `json:"startWindowMinutes,omitempty"`
	CompletionWindowMinutes *int64            `json:"completionWindowMinutes,omitempty"`
	Lifecycle               *Lifecycle        `json:"lifecycle,omitempty"`
	RecoveryPointTags       map[string]string `json:"recoveryPointTags,omitempty"`
	CopyActions             []CopyAction      `json:"copyActions,omitempty"`
	EnableContinuousBackup  *bool             `json:"enableContinuousBackup,omitempty"`
	RuleID                  string            `json:"ruleId,omitempty"`
}

// AdvancedBackupSetting carries per-resource-type backup options (for example
// Windows VSS). The block round-trips verbatim.
type AdvancedBackupSetting struct {
	ResourceType  string            `json:"resourceType,omitempty"`
	BackupOptions map[string]string `json:"backupOptions,omitempty"`
}

// PlanBody is the mutable body of a backup plan: the members a client supplies
// on create and update.
type PlanBody struct {
	BackupPlanName         string                  `json:"backupPlanName,omitempty"`
	Rules                  []Rule                  `json:"rules,omitempty"`
	AdvancedBackupSettings []AdvancedBackupSetting `json:"advancedBackupSettings,omitempty"`
}

// PlanVersion is one immutable version of a plan. A new version is appended on
// every UpdateBackupPlan; the last element is the current version.
type PlanVersion struct {
	VersionID    string    `json:"versionId"`
	CreationDate time.Time `json:"creationDate"`
	Body         PlanBody  `json:"body"`
}

// Plan is a backup plan and its full version history. BackupPlanID, BackupPlanArn
// and CreatorRequestID are minted once at create and stable across reads.
type Plan struct {
	BackupPlanID      string            `json:"backupPlanId"`
	BackupPlanArn     string            `json:"backupPlanArn"`
	CreatorRequestID  string            `json:"creatorRequestId,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	LastExecutionDate time.Time         `json:"lastExecutionDate,omitempty"`
	Versions          []PlanVersion     `json:"versions"`
}

// Current returns the plan's current (latest) version.
func (p *Plan) Current() PlanVersion {
	return p.Versions[len(p.Versions)-1]
}

// ConditionTag is a single tag-key/value condition in a selection's ListOfTags.
type ConditionTag struct {
	ConditionType  string `json:"conditionType,omitempty"`
	ConditionKey   string `json:"conditionKey,omitempty"`
	ConditionValue string `json:"conditionValue,omitempty"`
}

// ConditionParameter is a key/value pair inside a typed Conditions block.
type ConditionParameter struct {
	ConditionKey   string `json:"conditionKey,omitempty"`
	ConditionValue string `json:"conditionValue,omitempty"`
}

// Conditions is the typed condition block of a selection. Each list round-trips
// verbatim.
type Conditions struct {
	StringEquals    []ConditionParameter `json:"stringEquals,omitempty"`
	StringLike      []ConditionParameter `json:"stringLike,omitempty"`
	StringNotEquals []ConditionParameter `json:"stringNotEquals,omitempty"`
	StringNotLike   []ConditionParameter `json:"stringNotLike,omitempty"`
}

// SelectionBody is the mutable body of a backup selection.
type SelectionBody struct {
	SelectionName string         `json:"selectionName,omitempty"`
	IamRoleArn    string         `json:"iamRoleArn,omitempty"`
	Resources     []string       `json:"resources,omitempty"`
	NotResources  []string       `json:"notResources,omitempty"`
	ListOfTags    []ConditionTag `json:"listOfTags,omitempty"`
	Conditions    *Conditions    `json:"conditions,omitempty"`
}

// Selection is a backup selection assigned to a plan. SelectionID and
// CreationDate are minted once and stable across reads.
type Selection struct {
	SelectionID      string        `json:"selectionId"`
	BackupPlanID     string        `json:"backupPlanId"`
	CreatorRequestID string        `json:"creatorRequestId,omitempty"`
	CreationDate     time.Time     `json:"creationDate"`
	Body             SelectionBody `json:"body"`
}

// Vault is a backup vault and its access policy, notifications and Vault Lock
// state. BackupVaultArn and CreationDate are minted once and stable across
// reads.
type Vault struct {
	Name             string            `json:"name"`
	Arn              string            `json:"arn"`
	CreatorRequestID string            `json:"creatorRequestId,omitempty"`
	EncryptionKeyArn string            `json:"encryptionKeyArn,omitempty"`
	CreationDate     time.Time         `json:"creationDate"`
	Tags             map[string]string `json:"tags,omitempty"`

	// Access policy (JSON string) and event notifications.
	AccessPolicy      string   `json:"accessPolicy,omitempty"`
	SNSTopicArn       string   `json:"snsTopicArn,omitempty"`
	BackupVaultEvents []string `json:"backupVaultEvents,omitempty"`

	// Vault Lock state. Locked is true once a lock is applied. MinRetentionDays
	// and MaxRetentionDays are the enforced retention bounds. LockDate is set
	// only in COMPLIANCE mode (a lock created with ChangeableForDays): once now
	// is at or past LockDate the lock is immutable and the vault cannot be
	// deleted. A zero LockDate is GOVERNANCE mode, changeable at any time.
	Locked           bool       `json:"locked,omitempty"`
	MinRetentionDays *int64     `json:"minRetentionDays,omitempty"`
	MaxRetentionDays *int64     `json:"maxRetentionDays,omitempty"`
	LockDate         *time.Time `json:"lockDate,omitempty"`
}

// CreateVaultInput is the input to CreateBackupVault.
type CreateVaultInput struct {
	Name             string
	Tags             map[string]string
	CreatorRequestID string
	EncryptionKeyArn string
}

// PutVaultNotificationsInput is the input to PutBackupVaultNotifications.
type PutVaultNotificationsInput struct {
	Name              string
	SNSTopicArn       string
	BackupVaultEvents []string
}

// Notifications is the notification configuration of a vault.
type Notifications struct {
	SNSTopicArn       string
	BackupVaultEvents []string
}

// PutVaultLockInput is the input to PutBackupVaultLockConfiguration. A non-nil
// ChangeableForDays selects COMPLIANCE mode and fixes the LockDate that many
// days into the future.
type PutVaultLockInput struct {
	Name              string
	MinRetentionDays  *int64
	MaxRetentionDays  *int64
	ChangeableForDays *int64
}

// CreatePlanInput is the input to CreateBackupPlan.
type CreatePlanInput struct {
	Body             PlanBody
	Tags             map[string]string
	CreatorRequestID string
}

// UpdatePlanInput is the input to UpdateBackupPlan; it appends a new version.
type UpdatePlanInput struct {
	BackupPlanID string
	Body         PlanBody
}

// CreateSelectionInput is the input to CreateBackupSelection.
type CreateSelectionInput struct {
	BackupPlanID     string
	Body             SelectionBody
	CreatorRequestID string
}

// Backup is the AWS Backup control-plane surface.
type Backup interface {
	// Vaults.
	CreateBackupVault(ctx context.Context, in *CreateVaultInput) (*Vault, error)
	DescribeBackupVault(ctx context.Context, name string) (*Vault, error)
	DeleteBackupVault(ctx context.Context, name string) error
	ListBackupVaults(ctx context.Context, page Page) (vaults []*Vault, nextToken string, err error)

	// Vault access policy.
	PutBackupVaultAccessPolicy(ctx context.Context, name, policy string) error
	GetBackupVaultAccessPolicy(ctx context.Context, name string) (vault *Vault, policy string, err error)
	DeleteBackupVaultAccessPolicy(ctx context.Context, name string) error

	// Vault notifications.
	PutBackupVaultNotifications(ctx context.Context, in *PutVaultNotificationsInput) error
	GetBackupVaultNotifications(ctx context.Context, name string) (vault *Vault, n Notifications, err error)
	DeleteBackupVaultNotifications(ctx context.Context, name string) error

	// Vault Lock.
	PutBackupVaultLockConfiguration(ctx context.Context, in *PutVaultLockInput) error
	DeleteBackupVaultLockConfiguration(ctx context.Context, name string) error

	// Plans.
	CreateBackupPlan(ctx context.Context, in *CreatePlanInput) (*Plan, error)
	GetBackupPlan(ctx context.Context, id, versionID string) (*Plan, PlanVersion, error)
	UpdateBackupPlan(ctx context.Context, in *UpdatePlanInput) (*Plan, error)
	DeleteBackupPlan(ctx context.Context, id string) (*Plan, error)
	ListBackupPlans(ctx context.Context, page Page) (plans []*Plan, nextToken string, err error)
	ListBackupPlanVersions(ctx context.Context, id string, page Page) (plan *Plan, versions []PlanVersion, nextToken string, err error)

	// Selections.
	CreateBackupSelection(ctx context.Context, in *CreateSelectionInput) (*Selection, error)
	GetBackupSelection(ctx context.Context, planID, selectionID string) (*Selection, error)
	DeleteBackupSelection(ctx context.Context, planID, selectionID string) error
	ListBackupSelections(ctx context.Context, planID string, page Page) (selections []*Selection, nextToken string, err error)

	// Tagging.
	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTags(ctx context.Context, resourceArn string) (map[string]string, error)
}
