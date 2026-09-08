package backup

import "time"

// vaultStateAvailable and vaultTypeBackup are the only lifecycle state and vault
// type the emulator reports; there is no asynchronous provisioning.
const (
	vaultStateAvailable = "AVAILABLE"
	vaultTypeBackup     = "BACKUP_VAULT"
)

const millisPerSecond = 1000.0

// epochSeconds renders a timestamp as restJson1 epoch seconds (a JSON number),
// or nil for the zero time so an unset timestamp reads back as null.
func epochSeconds(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.UTC().UnixMilli()) / millisPerSecond

	return &secs
}

// --- wire shapes (PascalCase, matching the AWS Backup restJson1 protocol) ---

type lifecycleJSON struct {
	MoveToColdStorageAfterDays          *int64 `json:"MoveToColdStorageAfterDays,omitempty"`
	DeleteAfterDays                     *int64 `json:"DeleteAfterDays,omitempty"`
	OptInToArchiveForSupportedResources *bool  `json:"OptInToArchiveForSupportedResources,omitempty"`
}

type copyActionJSON struct {
	DestinationBackupVaultArn string         `json:"DestinationBackupVaultArn,omitempty"`
	Lifecycle                 *lifecycleJSON `json:"Lifecycle,omitempty"`
}

type ruleJSON struct {
	RuleName                   string            `json:"RuleName,omitempty"`
	TargetBackupVaultName      string            `json:"TargetBackupVaultName,omitempty"`
	ScheduleExpression         string            `json:"ScheduleExpression,omitempty"`
	ScheduleExpressionTimezone string            `json:"ScheduleExpressionTimezone,omitempty"`
	StartWindowMinutes         *int64            `json:"StartWindowMinutes,omitempty"`
	CompletionWindowMinutes    *int64            `json:"CompletionWindowMinutes,omitempty"`
	Lifecycle                  *lifecycleJSON    `json:"Lifecycle,omitempty"`
	RecoveryPointTags          map[string]string `json:"RecoveryPointTags,omitempty"`
	CopyActions                []copyActionJSON  `json:"CopyActions,omitempty"`
	EnableContinuousBackup     *bool             `json:"EnableContinuousBackup,omitempty"`
	RuleID                     string            `json:"RuleId,omitempty"`
}

type advancedSettingJSON struct {
	ResourceType  string            `json:"ResourceType,omitempty"`
	BackupOptions map[string]string `json:"BackupOptions,omitempty"`
}

type planBodyJSON struct {
	BackupPlanName         string                `json:"BackupPlanName,omitempty"`
	Rules                  []ruleJSON            `json:"Rules,omitempty"`
	AdvancedBackupSettings []advancedSettingJSON `json:"AdvancedBackupSettings,omitempty"`
}

type conditionParamJSON struct {
	ConditionKey   string `json:"ConditionKey,omitempty"`
	ConditionValue string `json:"ConditionValue,omitempty"`
}

type conditionsJSON struct {
	StringEquals    []conditionParamJSON `json:"StringEquals,omitempty"`
	StringLike      []conditionParamJSON `json:"StringLike,omitempty"`
	StringNotEquals []conditionParamJSON `json:"StringNotEquals,omitempty"`
	StringNotLike   []conditionParamJSON `json:"StringNotLike,omitempty"`
}

type conditionTagJSON struct {
	ConditionType  string `json:"ConditionType,omitempty"`
	ConditionKey   string `json:"ConditionKey,omitempty"`
	ConditionValue string `json:"ConditionValue,omitempty"`
}

type selectionJSON struct {
	SelectionName string             `json:"SelectionName,omitempty"`
	IamRoleArn    string             `json:"IamRoleArn,omitempty"`
	Resources     []string           `json:"Resources,omitempty"`
	NotResources  []string           `json:"NotResources,omitempty"`
	ListOfTags    []conditionTagJSON `json:"ListOfTags,omitempty"`
	Conditions    *conditionsJSON    `json:"Conditions,omitempty"`
}

// plansListMemberJSON is one entry in a ListBackupPlans/ListBackupPlanVersions
// response.
type plansListMemberJSON struct {
	BackupPlanArn          string                `json:"BackupPlanArn,omitempty"`
	BackupPlanID           string                `json:"BackupPlanId,omitempty"`
	BackupPlanName         string                `json:"BackupPlanName,omitempty"`
	CreationDate           *float64              `json:"CreationDate,omitempty"`
	VersionID              string                `json:"VersionId,omitempty"`
	CreatorRequestID       string                `json:"CreatorRequestId,omitempty"`
	LastExecutionDate      *float64              `json:"LastExecutionDate,omitempty"`
	AdvancedBackupSettings []advancedSettingJSON `json:"AdvancedBackupSettings,omitempty"`
}
