package backup

import "github.com/stackshy/cloudemu/v2/services/backup/driver"

func fromLifecycle(in *lifecycleJSON) *driver.Lifecycle {
	if in == nil {
		return nil
	}

	return &driver.Lifecycle{
		MoveToColdStorageAfterDays:          in.MoveToColdStorageAfterDays,
		DeleteAfterDays:                     in.DeleteAfterDays,
		OptInToArchiveForSupportedResources: in.OptInToArchiveForSupportedResources,
	}
}

func toLifecycle(in *driver.Lifecycle) *lifecycleJSON {
	if in == nil {
		return nil
	}

	return &lifecycleJSON{
		MoveToColdStorageAfterDays:          in.MoveToColdStorageAfterDays,
		DeleteAfterDays:                     in.DeleteAfterDays,
		OptInToArchiveForSupportedResources: in.OptInToArchiveForSupportedResources,
	}
}

func fromCopyActions(in []copyActionJSON) []driver.CopyAction {
	if in == nil {
		return nil
	}

	out := make([]driver.CopyAction, len(in))
	for i := range in {
		out[i] = driver.CopyAction{DestinationBackupVaultArn: in[i].DestinationBackupVaultArn, Lifecycle: fromLifecycle(in[i].Lifecycle)}
	}

	return out
}

func toCopyActions(in []driver.CopyAction) []copyActionJSON {
	if in == nil {
		return nil
	}

	out := make([]copyActionJSON, len(in))
	for i := range in {
		out[i] = copyActionJSON{DestinationBackupVaultArn: in[i].DestinationBackupVaultArn, Lifecycle: toLifecycle(in[i].Lifecycle)}
	}

	return out
}

func fromRules(in []ruleJSON) []driver.Rule {
	if in == nil {
		return nil
	}

	out := make([]driver.Rule, len(in))
	for i := range in {
		out[i] = driver.Rule{
			RuleName:                in[i].RuleName,
			TargetBackupVaultName:   in[i].TargetBackupVaultName,
			ScheduleExpression:      in[i].ScheduleExpression,
			ScheduleExpressionTZ:    in[i].ScheduleExpressionTimezone,
			StartWindowMinutes:      in[i].StartWindowMinutes,
			CompletionWindowMinutes: in[i].CompletionWindowMinutes,
			Lifecycle:               fromLifecycle(in[i].Lifecycle),
			RecoveryPointTags:       in[i].RecoveryPointTags,
			CopyActions:             fromCopyActions(in[i].CopyActions),
			EnableContinuousBackup:  in[i].EnableContinuousBackup,
		}
	}

	return out
}

func toRules(in []driver.Rule) []ruleJSON {
	if in == nil {
		return nil
	}

	out := make([]ruleJSON, len(in))
	for i := range in {
		out[i] = ruleJSON{
			RuleName:                   in[i].RuleName,
			TargetBackupVaultName:      in[i].TargetBackupVaultName,
			ScheduleExpression:         in[i].ScheduleExpression,
			ScheduleExpressionTimezone: in[i].ScheduleExpressionTZ,
			StartWindowMinutes:         in[i].StartWindowMinutes,
			CompletionWindowMinutes:    in[i].CompletionWindowMinutes,
			Lifecycle:                  toLifecycle(in[i].Lifecycle),
			RecoveryPointTags:          in[i].RecoveryPointTags,
			CopyActions:                toCopyActions(in[i].CopyActions),
			EnableContinuousBackup:     in[i].EnableContinuousBackup,
			RuleID:                     in[i].RuleID,
		}
	}

	return out
}

func fromAdvanced(in []advancedSettingJSON) []driver.AdvancedBackupSetting {
	if in == nil {
		return nil
	}

	out := make([]driver.AdvancedBackupSetting, len(in))
	for i := range in {
		out[i] = driver.AdvancedBackupSetting{ResourceType: in[i].ResourceType, BackupOptions: in[i].BackupOptions}
	}

	return out
}

func toAdvanced(in []driver.AdvancedBackupSetting) []advancedSettingJSON {
	if in == nil {
		return nil
	}

	out := make([]advancedSettingJSON, len(in))
	for i := range in {
		out[i] = advancedSettingJSON{ResourceType: in[i].ResourceType, BackupOptions: in[i].BackupOptions}
	}

	return out
}

func fromPlanBody(in *planBodyJSON) driver.PlanBody {
	return driver.PlanBody{
		BackupPlanName:         in.BackupPlanName,
		Rules:                  fromRules(in.Rules),
		AdvancedBackupSettings: fromAdvanced(in.AdvancedBackupSettings),
	}
}

func toPlanBody(in *driver.PlanBody) planBodyJSON {
	return planBodyJSON{
		BackupPlanName:         in.BackupPlanName,
		Rules:                  toRules(in.Rules),
		AdvancedBackupSettings: toAdvanced(in.AdvancedBackupSettings),
	}
}

func fromConditionParams(in []conditionParamJSON) []driver.ConditionParameter {
	if in == nil {
		return nil
	}

	out := make([]driver.ConditionParameter, len(in))
	for i := range in {
		out[i] = driver.ConditionParameter{ConditionKey: in[i].ConditionKey, ConditionValue: in[i].ConditionValue}
	}

	return out
}

func toConditionParams(in []driver.ConditionParameter) []conditionParamJSON {
	if in == nil {
		return nil
	}

	out := make([]conditionParamJSON, len(in))
	for i := range in {
		out[i] = conditionParamJSON{ConditionKey: in[i].ConditionKey, ConditionValue: in[i].ConditionValue}
	}

	return out
}

func fromConditions(in *conditionsJSON) *driver.Conditions {
	if in == nil {
		return nil
	}

	return &driver.Conditions{
		StringEquals:    fromConditionParams(in.StringEquals),
		StringLike:      fromConditionParams(in.StringLike),
		StringNotEquals: fromConditionParams(in.StringNotEquals),
		StringNotLike:   fromConditionParams(in.StringNotLike),
	}
}

func toConditions(in *driver.Conditions) *conditionsJSON {
	if in == nil {
		return nil
	}

	return &conditionsJSON{
		StringEquals:    toConditionParams(in.StringEquals),
		StringLike:      toConditionParams(in.StringLike),
		StringNotEquals: toConditionParams(in.StringNotEquals),
		StringNotLike:   toConditionParams(in.StringNotLike),
	}
}

func fromConditionTags(in []conditionTagJSON) []driver.ConditionTag {
	if in == nil {
		return nil
	}

	out := make([]driver.ConditionTag, len(in))
	for i := range in {
		out[i] = driver.ConditionTag{ConditionType: in[i].ConditionType, ConditionKey: in[i].ConditionKey, ConditionValue: in[i].ConditionValue}
	}

	return out
}

func toConditionTags(in []driver.ConditionTag) []conditionTagJSON {
	if in == nil {
		return nil
	}

	out := make([]conditionTagJSON, len(in))
	for i := range in {
		out[i] = conditionTagJSON{ConditionType: in[i].ConditionType, ConditionKey: in[i].ConditionKey, ConditionValue: in[i].ConditionValue}
	}

	return out
}

func fromSelectionBody(in *selectionJSON) driver.SelectionBody {
	return driver.SelectionBody{
		SelectionName: in.SelectionName,
		IamRoleArn:    in.IamRoleArn,
		Resources:     in.Resources,
		NotResources:  in.NotResources,
		ListOfTags:    fromConditionTags(in.ListOfTags),
		Conditions:    fromConditions(in.Conditions),
	}
}

func toSelectionBody(in *driver.SelectionBody) selectionJSON {
	return selectionJSON{
		SelectionName: in.SelectionName,
		IamRoleArn:    in.IamRoleArn,
		Resources:     in.Resources,
		NotResources:  in.NotResources,
		ListOfTags:    toConditionTags(in.ListOfTags),
		Conditions:    toConditions(in.Conditions),
	}
}

// toPlansListMember renders a plan's given version as a list-member entry.
func toPlansListMember(p *driver.Plan, v *driver.PlanVersion) plansListMemberJSON {
	return plansListMemberJSON{
		BackupPlanArn:          p.BackupPlanArn,
		BackupPlanID:           p.BackupPlanID,
		BackupPlanName:         v.Body.BackupPlanName,
		CreationDate:           epochSeconds(v.CreationDate),
		VersionID:              v.VersionID,
		CreatorRequestID:       p.CreatorRequestID,
		LastExecutionDate:      epochSeconds(p.LastExecutionDate),
		AdvancedBackupSettings: toAdvanced(v.Body.AdvancedBackupSettings),
	}
}
