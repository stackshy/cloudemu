package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// TestSDKRDSDescribeInstancesDbiResourceIDFilter guards that
// dbi-resource-id (the filter terraform-provider-aws polls right after
// CreateDBInstance) resolves an instance, and that an unrecognized filter
// name still errors rather than silently matching nothing.
func TestSDKRDSDescribeInstancesDbiResourceIDFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("dbi-filter-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	resourceID := aws.ToString(created.DBInstance.DbiResourceId)
	if resourceID == "" {
		t.Fatal("DbiResourceId empty; want a stable db- resource id")
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Filters: []rdstypes.Filter{{Name: aws.String("dbi-resource-id"), Values: []string{resourceID}}},
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances(dbi-resource-id): %v", err)
	}

	if len(got.DBInstances) != 1 || aws.ToString(got.DBInstances[0].DBInstanceIdentifier) != "dbi-filter-db" {
		t.Fatalf("dbi-resource-id filter returned %d instances, want only dbi-filter-db", len(got.DBInstances))
	}

	_, err = client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Filters: []rdstypes.Filter{{Name: aws.String("not-a-real-filter"), Values: []string{"x"}}},
	})
	if err == nil {
		t.Fatal("unknown filter succeeded, want an error")
	}
}

// TestSDKRDSCreateInstanceParamsRoundTrip guards that BackupRetentionPeriod,
// PreferredBackupWindow, PreferredMaintenanceWindow and
// AutoMinorVersionUpgrade all echo back exactly what CreateDBInstance was
// given, not a silently-defaulted value.
func TestSDKRDSCreateInstanceParamsRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier:       aws.String("params-db"),
		Engine:                     aws.String("mysql"),
		DBInstanceClass:            aws.String("db.t3.micro"),
		AllocatedStorage:           aws.Int32(20),
		BackupRetentionPeriod:      aws.Int32(7),
		PreferredBackupWindow:      aws.String("03:00-03:30"),
		PreferredMaintenanceWindow: aws.String("tue:04:00-tue:04:30"),
		AutoMinorVersionUpgrade:    aws.Bool(false),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("params-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(got.DBInstances) != 1 {
		t.Fatalf("got %d instances, want 1", len(got.DBInstances))
	}

	inst := got.DBInstances[0]

	if aws.ToInt32(inst.BackupRetentionPeriod) != 7 {
		t.Errorf("BackupRetentionPeriod = %d, want 7", aws.ToInt32(inst.BackupRetentionPeriod))
	}

	if got := aws.ToString(inst.PreferredBackupWindow); got != "03:00-03:30" {
		t.Errorf("PreferredBackupWindow = %q, want 03:00-03:30", got)
	}

	if got := aws.ToString(inst.PreferredMaintenanceWindow); got != "tue:04:00-tue:04:30" {
		t.Errorf("PreferredMaintenanceWindow = %q, want tue:04:00-tue:04:30", got)
	}

	if aws.ToBool(inst.AutoMinorVersionUpgrade) {
		t.Error("AutoMinorVersionUpgrade = true, want false (explicit request value)")
	}
}

// TestSDKRDSBackupRetentionPeriodExplicitZero is the regression guard for the
// real-user divergence: terraform-provider-aws's aws_db_instance schema
// default for backup_retention_period is 0 (disable automated backups), so it
// ALWAYS sends an explicit 0 on a default apply. An explicit 0 must survive
// as 0, distinct from an omitted parameter which real RDS defaults to 1.
func TestSDKRDSBackupRetentionPeriodExplicitZero(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier:  aws.String("zero-retention-db"),
		Engine:                aws.String("mysql"),
		DBInstanceClass:       aws.String("db.t3.micro"),
		AllocatedStorage:      aws.Int32(20),
		BackupRetentionPeriod: aws.Int32(0),
	}); err != nil {
		t.Fatalf("CreateDBInstance(explicit 0): %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("omitted-retention-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance(omitted): %v", err)
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("zero-retention-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances(zero): %v", err)
	}

	if len(got.DBInstances) != 1 {
		t.Fatalf("zero-retention-db: got %d instances, want 1", len(got.DBInstances))
	}

	if got.DBInstances[0].BackupRetentionPeriod == nil || *got.DBInstances[0].BackupRetentionPeriod != 0 {
		t.Fatalf("explicit BackupRetentionPeriod=0 became %v, want 0 (not coerced to the default)",
			got.DBInstances[0].BackupRetentionPeriod)
	}

	gotOmit, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("omitted-retention-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances(omitted): %v", err)
	}

	if len(gotOmit.DBInstances) != 1 {
		t.Fatalf("omitted-retention-db: got %d instances, want 1", len(gotOmit.DBInstances))
	}

	if aws.ToInt32(gotOmit.DBInstances[0].BackupRetentionPeriod) != 1 {
		t.Fatalf("omitted BackupRetentionPeriod = %d, want the AWS default 1",
			aws.ToInt32(gotOmit.DBInstances[0].BackupRetentionPeriod))
	}
}

// TestSDKRDSRestoreInstanceFromSnapshotParams guards that
// RestoreDBInstanceFromDBSnapshot reports a populated DbiResourceId and
// applies AutoMinorVersionUpgrade from the restore request rather than
// hardcoding true.
func TestSDKRDSRestoreInstanceFromSnapshotParams(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("restore-params-src"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("restore-params-snap"),
		DBInstanceIdentifier: aws.String("restore-params-src"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	restored, err := client.RestoreDBInstanceFromDBSnapshot(ctx,
		&awsrds.RestoreDBInstanceFromDBSnapshotInput{
			DBInstanceIdentifier:    aws.String("restore-params-restored"),
			DBSnapshotIdentifier:    aws.String("restore-params-snap"),
			AutoMinorVersionUpgrade: aws.Bool(false),
		})
	if err != nil {
		t.Fatalf("RestoreDBInstanceFromDBSnapshot: %v", err)
	}

	if aws.ToString(restored.DBInstance.DbiResourceId) == "" {
		t.Error("restored DbiResourceId empty; want a stable db- resource id")
	}

	if aws.ToBool(restored.DBInstance.AutoMinorVersionUpgrade) {
		t.Error("restored AutoMinorVersionUpgrade = true, want false (explicit restore request value)")
	}

	// Omitting the parameter on a restore falls back to the real AWS default (true).
	if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("restore-params-snap2"),
		DBInstanceIdentifier: aws.String("restore-params-src"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot(2): %v", err)
	}

	restoredDefault, err := client.RestoreDBInstanceFromDBSnapshot(ctx,
		&awsrds.RestoreDBInstanceFromDBSnapshotInput{
			DBInstanceIdentifier: aws.String("restore-params-restored-default"),
			DBSnapshotIdentifier: aws.String("restore-params-snap2"),
		})
	if err != nil {
		t.Fatalf("RestoreDBInstanceFromDBSnapshot(default): %v", err)
	}

	if !aws.ToBool(restoredDefault.DBInstance.AutoMinorVersionUpgrade) {
		t.Error("restored AutoMinorVersionUpgrade = false, want true (default when omitted)")
	}
}

// TestSDKRDSCreateDBSubnetGroupTags guards that Tags supplied on
// CreateDBSubnetGroup are stored and readable via ListTagsForResource.
func TestSDKRDSCreateDBSubnetGroupTags(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	_, subnetIDs := mkSubnets(t, ec2c)

	created, err := rdsc.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("tagged-sng"),
		DBSubnetGroupDescription: aws.String("tagged group"),
		SubnetIds:                subnetIDs,
		Tags:                     []rdstypes.Tag{{Key: aws.String("team"), Value: aws.String("db")}},
	})
	if err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	tags, err := rdsc.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: created.DBSubnetGroup.DBSubnetGroupArn,
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.TagList) != 1 || aws.ToString(tags.TagList[0].Key) != "team" ||
		aws.ToString(tags.TagList[0].Value) != "db" {
		t.Fatalf("subnet-group tags = %+v, want team=db", tags.TagList)
	}
}

// TestSDKRDSModifyDBSubnetGroup guards that ModifyDBSubnetGroup replaces
// subnet membership (re-resolving VpcId) and description, and that modifying
// an unknown group name reports DBSubnetGroupNotFoundFault.
func TestSDKRDSModifyDBSubnetGroup(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	_, firstSubnets := mkSubnets(t, ec2c)
	secondVPCID, secondSubnets := mkSubnets(t, ec2c)

	if _, err := rdsc.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("modify-sng"),
		DBSubnetGroupDescription: aws.String("original"),
		SubnetIds:                firstSubnets,
	}); err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	modified, err := rdsc.ModifyDBSubnetGroup(ctx, &awsrds.ModifyDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("modify-sng"),
		DBSubnetGroupDescription: aws.String("updated"),
		SubnetIds:                secondSubnets,
	})
	if err != nil {
		t.Fatalf("ModifyDBSubnetGroup: %v", err)
	}

	if got := aws.ToString(modified.DBSubnetGroup.DBSubnetGroupDescription); got != "updated" {
		t.Errorf("description = %q, want updated", got)
	}

	if got := aws.ToString(modified.DBSubnetGroup.VpcId); got != secondVPCID {
		t.Errorf("VpcId = %q, want %q (moved to the new subnets' VPC)", got, secondVPCID)
	}

	if n := len(modified.DBSubnetGroup.Subnets); n != len(secondSubnets) {
		t.Errorf("subnets = %d, want %d", n, len(secondSubnets))
	}

	_, err = rdsc.ModifyDBSubnetGroup(ctx, &awsrds.ModifyDBSubnetGroupInput{
		DBSubnetGroupName: aws.String("no-such-sng"),
		SubnetIds:         secondSubnets,
	})

	var notFound *rdstypes.DBSubnetGroupNotFoundFault
	if !errors.As(err, &notFound) {
		t.Errorf("modifying an unknown group: got %v, want DBSubnetGroupNotFoundFault", err)
	}
}

// TestSDKRDSCopyDBSnapshotSourceIdentifierEmpty is the regression guard: per
// the AWS DBSnapshot API docs, SourceDBSnapshotIdentifier "only has a value in
// the case of a cross-account or cross-Region copy." cloudemu models only
// same-account/same-region copies, so the field must stay empty on every copy.
func TestSDKRDSCopyDBSnapshotSourceIdentifierEmpty(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("copy-src-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
		DBSnapshotIdentifier: aws.String("copy-src-snap"),
		DBInstanceIdentifier: aws.String("copy-src-db"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	copied, err := client.CopyDBSnapshot(ctx, &awsrds.CopyDBSnapshotInput{
		SourceDBSnapshotIdentifier: aws.String("copy-src-snap"),
		TargetDBSnapshotIdentifier: aws.String("copy-dst-snap"),
	})
	if err != nil {
		t.Fatalf("CopyDBSnapshot: %v", err)
	}

	if got := aws.ToString(copied.DBSnapshot.SourceDBSnapshotIdentifier); got != "" {
		t.Errorf("SourceDBSnapshotIdentifier = %q, want empty (same-account/same-region copy)", got)
	}

	got, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String("copy-dst-snap"),
	})
	if err != nil {
		t.Fatalf("DescribeDBSnapshots: %v", err)
	}

	if len(got.DBSnapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(got.DBSnapshots))
	}

	if got := aws.ToString(got.DBSnapshots[0].SourceDBSnapshotIdentifier); got != "" {
		t.Errorf("described SourceDBSnapshotIdentifier = %q, want empty", got)
	}
}
