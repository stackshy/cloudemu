package backup_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	backupapi "github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *backupapi.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Backup: cloud.Backup})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return backupapi.NewFromConfig(cfg, func(o *backupapi.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func mustCreateVault(t *testing.T, c *backupapi.Client, name string) *backupapi.CreateBackupVaultOutput {
	t.Helper()

	out, err := c.CreateBackupVault(context.Background(), &backupapi.CreateBackupVaultInput{
		BackupVaultName: aws.String(name),
		BackupVaultTags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateBackupVault: %v", err)
	}

	return out
}

func TestVaultLifecycleAndByteStability(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	created := mustCreateVault(t, c, "my-vault")

	d1, err := c.DescribeBackupVault(ctx, &backupapi.DescribeBackupVaultInput{BackupVaultName: aws.String("my-vault")})
	if err != nil {
		t.Fatalf("DescribeBackupVault: %v", err)
	}

	d2, err := c.DescribeBackupVault(ctx, &backupapi.DescribeBackupVaultInput{BackupVaultName: aws.String("my-vault")})
	if err != nil {
		t.Fatalf("DescribeBackupVault #2: %v", err)
	}

	// Byte-stability: ARN and CreationDate are minted once and never drift.
	if aws.ToString(created.BackupVaultArn) != aws.ToString(d1.BackupVaultArn) {
		t.Fatalf("arn drift: create %q describe %q", aws.ToString(created.BackupVaultArn), aws.ToString(d1.BackupVaultArn))
	}

	if aws.ToString(d1.BackupVaultArn) != aws.ToString(d2.BackupVaultArn) {
		t.Fatalf("arn not stable across describes")
	}

	if !aws.ToTime(d1.CreationDate).Equal(aws.ToTime(d2.CreationDate)) {
		t.Fatalf("CreationDate not stable: %v vs %v", aws.ToTime(d1.CreationDate), aws.ToTime(d2.CreationDate))
	}

	want := "arn:aws:backup:us-east-1:123456789012:backup-vault:my-vault"
	if got := aws.ToString(d1.BackupVaultArn); got != want {
		t.Fatalf("vault arn = %q, want %q", got, want)
	}

	list, err := c.ListBackupVaults(ctx, &backupapi.ListBackupVaultsInput{})
	if err != nil || len(list.BackupVaultList) != 1 {
		t.Fatalf("ListBackupVaults: err=%v n=%d", err, len(list.BackupVaultList))
	}
}

func TestVaultLockGovernance(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	mustCreateVault(t, c, "locked-vault")

	// GOVERNANCE mode: no ChangeableForDays → Locked but no immutable lock date.
	_, err := c.PutBackupVaultLockConfiguration(ctx, &backupapi.PutBackupVaultLockConfigurationInput{
		BackupVaultName:  aws.String("locked-vault"),
		MinRetentionDays: aws.Int64(7),
		MaxRetentionDays: aws.Int64(1000),
	})
	if err != nil {
		t.Fatalf("PutBackupVaultLockConfiguration: %v", err)
	}

	d, err := c.DescribeBackupVault(ctx, &backupapi.DescribeBackupVaultInput{BackupVaultName: aws.String("locked-vault")})
	if err != nil {
		t.Fatalf("DescribeBackupVault: %v", err)
	}

	if !aws.ToBool(d.Locked) {
		t.Fatalf("vault should report Locked=true")
	}

	if aws.ToInt64(d.MinRetentionDays) != 7 || aws.ToInt64(d.MaxRetentionDays) != 1000 {
		t.Fatalf("retention bounds not reported: min=%d max=%d", aws.ToInt64(d.MinRetentionDays), aws.ToInt64(d.MaxRetentionDays))
	}

	if d.LockDate != nil {
		t.Fatalf("GOVERNANCE lock must have no LockDate, got %v", aws.ToTime(d.LockDate))
	}

	// GOVERNANCE lock is changeable: removing it succeeds.
	if _, err := c.DeleteBackupVaultLockConfiguration(ctx,
		&backupapi.DeleteBackupVaultLockConfigurationInput{BackupVaultName: aws.String("locked-vault")}); err != nil {
		t.Fatalf("DeleteBackupVaultLockConfiguration: %v", err)
	}

	d2, _ := c.DescribeBackupVault(ctx, &backupapi.DescribeBackupVaultInput{BackupVaultName: aws.String("locked-vault")})
	if aws.ToBool(d2.Locked) {
		t.Fatalf("lock should be removed")
	}
}

func TestVaultAccessPolicyAndNotifications(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	mustCreateVault(t, c, "policy-vault")

	policy := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutBackupVaultAccessPolicy(ctx, &backupapi.PutBackupVaultAccessPolicyInput{
		BackupVaultName: aws.String("policy-vault"), Policy: aws.String(policy),
	}); err != nil {
		t.Fatalf("PutBackupVaultAccessPolicy: %v", err)
	}

	gp, err := c.GetBackupVaultAccessPolicy(ctx, &backupapi.GetBackupVaultAccessPolicyInput{BackupVaultName: aws.String("policy-vault")})
	if err != nil || aws.ToString(gp.Policy) != policy {
		t.Fatalf("GetBackupVaultAccessPolicy: err=%v policy=%q", err, aws.ToString(gp.Policy))
	}

	if _, err := c.DeleteBackupVaultAccessPolicy(ctx,
		&backupapi.DeleteBackupVaultAccessPolicyInput{BackupVaultName: aws.String("policy-vault")}); err != nil {
		t.Fatalf("DeleteBackupVaultAccessPolicy: %v", err)
	}

	if _, err := c.PutBackupVaultNotifications(ctx, &backupapi.PutBackupVaultNotificationsInput{
		BackupVaultName:   aws.String("policy-vault"),
		SNSTopicArn:       aws.String("arn:aws:sns:us-east-1:123456789012:backup-topic"),
		BackupVaultEvents: []backuptypes.BackupVaultEvent{backuptypes.BackupVaultEventBackupJobCompleted},
	}); err != nil {
		t.Fatalf("PutBackupVaultNotifications: %v", err)
	}

	gn, err := c.GetBackupVaultNotifications(ctx, &backupapi.GetBackupVaultNotificationsInput{BackupVaultName: aws.String("policy-vault")})
	if err != nil || len(gn.BackupVaultEvents) != 1 {
		t.Fatalf("GetBackupVaultNotifications: err=%v events=%v", err, gn.BackupVaultEvents)
	}
}

func TestVaultDeleteThen404(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	mustCreateVault(t, c, "ephemeral")

	if _, err := c.DeleteBackupVault(ctx, &backupapi.DeleteBackupVaultInput{BackupVaultName: aws.String("ephemeral")}); err != nil {
		t.Fatalf("DeleteBackupVault: %v", err)
	}

	_, err := c.DescribeBackupVault(ctx, &backupapi.DescribeBackupVaultInput{BackupVaultName: aws.String("ephemeral")})

	var nf *backuptypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %v", err)
	}
}

func samplePlanInput(name string) *backuptypes.BackupPlanInput {
	return &backuptypes.BackupPlanInput{
		BackupPlanName: aws.String(name),
		Rules: []backuptypes.BackupRuleInput{{
			RuleName:              aws.String("daily"),
			TargetBackupVaultName: aws.String("plan-vault"),
			ScheduleExpression:    aws.String("cron(0 5 ? * * *)"),
			StartWindowMinutes:    aws.Int64(60),
			Lifecycle: &backuptypes.Lifecycle{
				DeleteAfterDays:            aws.Int64(30),
				MoveToColdStorageAfterDays: aws.Int64(10),
			},
			RecoveryPointTags: map[string]string{"tier": "gold"},
		}},
	}
}

func TestPlanVersioningAndSelectionGuard(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	mustCreateVault(t, c, "plan-vault")

	created, err := c.CreateBackupPlan(ctx, &backupapi.CreateBackupPlanInput{BackupPlan: samplePlanInput("nightly")})
	if err != nil {
		t.Fatalf("CreateBackupPlan: %v", err)
	}

	planID := aws.ToString(created.BackupPlanId)
	v1 := aws.ToString(created.VersionId)

	got, err := c.GetBackupPlan(ctx, &backupapi.GetBackupPlanInput{BackupPlanId: aws.String(planID)})
	if err != nil {
		t.Fatalf("GetBackupPlan: %v", err)
	}

	if len(got.BackupPlan.Rules) != 1 || aws.ToString(got.BackupPlan.Rules[0].RuleName) != "daily" {
		t.Fatalf("plan body did not round-trip: %+v", got.BackupPlan)
	}

	if aws.ToInt64(got.BackupPlan.Rules[0].Lifecycle.DeleteAfterDays) != 30 {
		t.Fatalf("lifecycle did not round-trip verbatim")
	}

	upd, err := c.UpdateBackupPlan(ctx, &backupapi.UpdateBackupPlanInput{
		BackupPlanId: aws.String(planID), BackupPlan: samplePlanInput("nightly-v2"),
	})
	if err != nil {
		t.Fatalf("UpdateBackupPlan: %v", err)
	}

	v2 := aws.ToString(upd.VersionId)
	if v2 == v1 {
		t.Fatalf("UpdateBackupPlan must bump VersionId (v1=%q v2=%q)", v1, v2)
	}

	if aws.ToString(upd.BackupPlanId) != planID {
		t.Fatalf("BackupPlanId must be stable across update")
	}

	// The old version is still resolvable by id.
	oldVer, err := c.GetBackupPlan(ctx, &backupapi.GetBackupPlanInput{BackupPlanId: aws.String(planID), VersionId: aws.String(v1)})
	if err != nil || aws.ToString(oldVer.BackupPlan.BackupPlanName) != "nightly" {
		t.Fatalf("GetBackupPlan by old versionId: err=%v name=%q", err, aws.ToString(oldVer.BackupPlan.BackupPlanName))
	}

	versions, err := c.ListBackupPlanVersions(ctx, &backupapi.ListBackupPlanVersionsInput{BackupPlanId: aws.String(planID)})
	if err != nil || len(versions.BackupPlanVersionsList) != 2 {
		t.Fatalf("ListBackupPlanVersions: err=%v n=%d", err, len(versions.BackupPlanVersionsList))
	}

	assertSelectionGuard(t, c, planID)
}

func assertSelectionGuard(t *testing.T, c *backupapi.Client, planID string) {
	t.Helper()
	ctx := context.Background()

	sel, err := c.CreateBackupSelection(ctx, &backupapi.CreateBackupSelectionInput{
		BackupPlanId: aws.String(planID),
		BackupSelection: &backuptypes.BackupSelection{
			SelectionName: aws.String("all-ebs"),
			IamRoleArn:    aws.String("arn:aws:iam::123456789012:role/backup"),
			Resources:     []string{"arn:aws:ec2:us-east-1:123456789012:volume/*"},
		},
	})
	if err != nil {
		t.Fatalf("CreateBackupSelection: %v", err)
	}

	selID := aws.ToString(sel.SelectionId)

	get, err := c.GetBackupSelection(ctx, &backupapi.GetBackupSelectionInput{
		BackupPlanId: aws.String(planID), SelectionId: aws.String(selID),
	})
	if err != nil || aws.ToString(get.BackupSelection.SelectionName) != "all-ebs" {
		t.Fatalf("GetBackupSelection: err=%v name=%q", err, aws.ToString(get.BackupSelection.SelectionName))
	}

	// Delete-guard: a plan referenced by a selection cannot be deleted.
	_, err = c.DeleteBackupPlan(ctx, &backupapi.DeleteBackupPlanInput{BackupPlanId: aws.String(planID)})

	var ire *backuptypes.InvalidRequestException
	if !errors.As(err, &ire) {
		t.Fatalf("expected InvalidRequestException deleting a referenced plan, got %v", err)
	}

	if _, err := c.DeleteBackupSelection(ctx, &backupapi.DeleteBackupSelectionInput{
		BackupPlanId: aws.String(planID), SelectionId: aws.String(selID),
	}); err != nil {
		t.Fatalf("DeleteBackupSelection: %v", err)
	}

	// Now the plan deletes cleanly.
	if _, err := c.DeleteBackupPlan(ctx, &backupapi.DeleteBackupPlanInput{BackupPlanId: aws.String(planID)}); err != nil {
		t.Fatalf("DeleteBackupPlan after removing selection: %v", err)
	}
}

func TestTagging(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	v := mustCreateVault(t, c, "tag-vault")
	arn := aws.ToString(v.BackupVaultArn)

	if _, err := c.TagResource(ctx, &backupapi.TagResourceInput{
		ResourceArn: aws.String(arn), Tags: map[string]string{"team": "sre"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTags(ctx, &backupapi.ListTagsInput{ResourceArn: aws.String(arn)})
	if err != nil || lt.Tags["team"] != "sre" || lt.Tags["env"] != "test" {
		t.Fatalf("ListTags: err=%v tags=%v", err, lt.Tags)
	}

	if _, err := c.UntagResource(ctx, &backupapi.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeyList: []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTags(ctx, &backupapi.ListTagsInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["team"]; ok {
		t.Fatalf("tag 'team' should be removed, got %v", lt2.Tags)
	}
}
