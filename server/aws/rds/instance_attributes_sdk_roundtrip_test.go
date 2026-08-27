package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

// TestSDKRDSInstanceCreateAttributes asserts a freshly created DBInstance
// reports the descriptive attributes real RDS defaults — DbiResourceId, the CA
// certificate id, the backup-retention default, the endpoint hosted-zone id,
// and the associated parameter group as an in-sync membership.
func TestSDKRDSInstanceCreateAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("attrs"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		DBParameterGroupName: aws.String("default.mysql8.0"),
		StorageEncrypted:     aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	inst := out.DBInstance

	if aws.ToString(inst.DbiResourceId) == "" {
		t.Fatal("DbiResourceId empty; want a stable db- resource id")
	}

	if aws.ToString(inst.CACertificateIdentifier) != "rds-ca-rsa2048-g1" {
		t.Fatalf("CACertificateIdentifier=%q, want rds-ca-rsa2048-g1",
			aws.ToString(inst.CACertificateIdentifier))
	}

	if aws.ToInt32(inst.BackupRetentionPeriod) != 1 {
		t.Fatalf("BackupRetentionPeriod=%d, want 1", aws.ToInt32(inst.BackupRetentionPeriod))
	}

	if !aws.ToBool(inst.StorageEncrypted) {
		t.Fatal("StorageEncrypted=false, want true")
	}

	if inst.Endpoint == nil || aws.ToString(inst.Endpoint.HostedZoneId) == "" {
		t.Fatal("Endpoint.HostedZoneId empty; want a hosted-zone id")
	}

	if len(inst.DBParameterGroups) != 1 ||
		aws.ToString(inst.DBParameterGroups[0].DBParameterGroupName) != "default.mysql8.0" ||
		aws.ToString(inst.DBParameterGroups[0].ParameterApplyStatus) != "in-sync" {
		t.Fatalf("DBParameterGroups=%+v, want one in-sync default.mysql8.0", inst.DBParameterGroups)
	}
}

// TestSDKRDSModifyInstanceAttributes asserts ModifyDBInstance honors the scalar
// attributes real RDS applies — BackupRetentionPeriod, backup/maintenance
// windows, StorageType, Iops and DeletionProtection — and reports them back.
func TestSDKRDSModifyInstanceAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("moddb"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	out, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier:       aws.String("moddb"),
		BackupRetentionPeriod:      aws.Int32(7),
		PreferredBackupWindow:      aws.String("02:00-03:00"),
		PreferredMaintenanceWindow: aws.String("mon:04:00-mon:05:00"),
		StorageType:                aws.String("io1"),
		Iops:                       aws.Int32(3000),
		DeletionProtection:         aws.Bool(true),
		ApplyImmediately:           aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	inst := out.DBInstance

	if aws.ToInt32(inst.BackupRetentionPeriod) != 7 {
		t.Fatalf("BackupRetentionPeriod=%d, want 7", aws.ToInt32(inst.BackupRetentionPeriod))
	}

	if aws.ToString(inst.PreferredBackupWindow) != "02:00-03:00" {
		t.Fatalf("PreferredBackupWindow=%q, want 02:00-03:00", aws.ToString(inst.PreferredBackupWindow))
	}

	if aws.ToString(inst.PreferredMaintenanceWindow) != "mon:04:00-mon:05:00" {
		t.Fatalf("PreferredMaintenanceWindow=%q", aws.ToString(inst.PreferredMaintenanceWindow))
	}

	if aws.ToString(inst.StorageType) != "io1" {
		t.Fatalf("StorageType=%q, want io1", aws.ToString(inst.StorageType))
	}

	if aws.ToInt32(inst.Iops) != 3000 {
		t.Fatalf("Iops=%d, want 3000", aws.ToInt32(inst.Iops))
	}

	if !aws.ToBool(inst.DeletionProtection) {
		t.Fatal("DeletionProtection=false, want true")
	}
}

// TestSDKRDSRenameInstance asserts ModifyDBInstance with NewDBInstanceIdentifier
// renames the instance: the new id is described and the old id is gone.
func TestSDKRDSRenameInstance(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("oldname"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	out, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier:    aws.String("oldname"),
		NewDBInstanceIdentifier: aws.String("newname"),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance rename: %v", err)
	}

	if aws.ToString(out.DBInstance.DBInstanceIdentifier) != "newname" {
		t.Fatalf("rename returned id %q, want newname", aws.ToString(out.DBInstance.DBInstanceIdentifier))
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("newname"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances(newname): %v", err)
	}

	if len(got.DBInstances) != 1 {
		t.Fatalf("got %d instances for newname, want 1", len(got.DBInstances))
	}

	if _, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("oldname"),
	}); err == nil {
		t.Fatal("expected oldname to be gone after rename")
	}
}

// TestSDKRDSModifyPendingModifiedValues asserts a deferred ModifyDBInstance
// (ApplyImmediately=false) leaves the current values unchanged and reports the
// requested changes in the nested PendingModifiedValues element on
// DescribeDBInstances, with the master password masked.
func TestSDKRDSModifyPendingModifiedValues(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("pending"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	out, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("pending"),
		DBInstanceClass:      aws.String("db.t3.large"),
		AllocatedStorage:     aws.Int32(50),
		MasterUserPassword:   aws.String("SuperSecret123!"),
		ApplyImmediately:     aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	// The instance keeps its current class/storage; the change is only pending.
	if aws.ToString(out.DBInstance.DBInstanceClass) != "db.t3.micro" {
		t.Fatalf("class applied immediately=%q, want unchanged db.t3.micro", aws.ToString(out.DBInstance.DBInstanceClass))
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("pending"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(got.DBInstances) != 1 {
		t.Fatalf("got %d instances, want 1", len(got.DBInstances))
	}

	inst := got.DBInstances[0]

	if aws.ToString(inst.DBInstanceClass) != "db.t3.micro" || aws.ToInt32(inst.AllocatedStorage) != 20 {
		t.Fatalf("current values changed: class=%q storage=%d, want db.t3.micro/20",
			aws.ToString(inst.DBInstanceClass), aws.ToInt32(inst.AllocatedStorage))
	}

	pmv := inst.PendingModifiedValues
	if pmv == nil {
		t.Fatal("PendingModifiedValues nil; want the deferred changes")
	}

	if aws.ToString(pmv.DBInstanceClass) != "db.t3.large" {
		t.Fatalf("PendingModifiedValues.DBInstanceClass=%q, want db.t3.large", aws.ToString(pmv.DBInstanceClass))
	}

	if aws.ToInt32(pmv.AllocatedStorage) != 50 {
		t.Fatalf("PendingModifiedValues.AllocatedStorage=%d, want 50", aws.ToInt32(pmv.AllocatedStorage))
	}

	if aws.ToString(pmv.MasterUserPassword) != "****" {
		t.Fatalf("PendingModifiedValues.MasterUserPassword=%q, want masked ****", aws.ToString(pmv.MasterUserPassword))
	}
}

// TestSDKRDSModifyApplyImmediatelyClearsPending asserts ApplyImmediately=true
// updates the instance now and reports no PendingModifiedValues.
func TestSDKRDSModifyApplyImmediatelyClearsPending(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("applynow"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	out, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("applynow"),
		DBInstanceClass:      aws.String("db.t3.large"),
		ApplyImmediately:     aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	if aws.ToString(out.DBInstance.DBInstanceClass) != "db.t3.large" {
		t.Fatalf("class=%q, want db.t3.large applied now", aws.ToString(out.DBInstance.DBInstanceClass))
	}

	if out.DBInstance.PendingModifiedValues != nil &&
		aws.ToString(out.DBInstance.PendingModifiedValues.DBInstanceClass) != "" {
		t.Fatalf("expected no pending class, got %q",
			aws.ToString(out.DBInstance.PendingModifiedValues.DBInstanceClass))
	}
}
