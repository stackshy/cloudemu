package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"
)

// TestSDKRDSDefaultGroupUpgradeAndImmutability covers, over the wire, a major
// version upgrade moving the instance to the new default group, a modify of a
// listed default group answering InvalidParameterValue (not NotFound), and an
// event subscription's ARN being taggable.
func TestSDKRDSDefaultGroupUpgradeAndImmutability(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("up1"), Engine: aws.String("postgres"),
		EngineVersion: aws.String("15.4"), DBInstanceClass: aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	mod, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("up1"), EngineVersion: aws.String("16.2"),
		AllowMajorVersionUpgrade: aws.Bool(true), ApplyImmediately: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	if g := aws.ToString(mod.DBInstance.DBParameterGroups[0].DBParameterGroupName); g != "default.postgres16" {
		t.Fatalf("parameter group after upgrade = %q, want default.postgres16", g)
	}

	_, err = client.ModifyDBParameterGroup(ctx, &awsrds.ModifyDBParameterGroupInput{
		DBParameterGroupName: aws.String("default.postgres16"),
		Parameters: []rdstypes.Parameter{{
			ParameterName: aws.String("max_connections"), ParameterValue: aws.String("10"),
			ApplyMethod: rdstypes.ApplyMethodImmediate,
		}},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("modify default group err = %v, want InvalidParameterValue", err)
	}

	sub, err := client.CreateEventSubscription(ctx, &awsrds.CreateEventSubscriptionInput{
		SubscriptionName: aws.String("tfsub"),
		SnsTopicArn:      aws.String("arn:aws:sns:us-east-1:000000000000:t"),
		Tags:             []rdstypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	})
	if err != nil {
		t.Fatalf("CreateEventSubscription: %v", err)
	}

	tags, err := client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: sub.EventSubscription.EventSubscriptionArn,
	})
	if err != nil || len(tags.TagList) != 1 || aws.ToString(tags.TagList[0].Value) != "dev" {
		t.Fatalf("ListTagsForResource(es ARN) = %+v, err %v", tags, err)
	}
}

// TestSDKRDSUpgradeRejectsParamGroupFamilyMismatch: a major upgrade of an
// instance on a custom old-family group, or an explicit group of the wrong
// family, is InvalidParameterCombination over the wire.
func TestSDKRDSUpgradeRejectsParamGroupFamilyMismatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for name, family := range map[string]string{"my15": "postgres15", "my16": "postgres16"} {
		if _, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
			DBParameterGroupName: aws.String(name), DBParameterGroupFamily: aws.String(family),
			Description: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateDBParameterGroup %s: %v", name, err)
		}
	}

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("fam1"), Engine: aws.String("postgres"),
		EngineVersion: aws.String("15.4"), DBInstanceClass: aws.String("db.t3.micro"),
		DBParameterGroupName: aws.String("my15"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	for _, in := range []*awsrds.ModifyDBInstanceInput{
		{EngineVersion: aws.String("16.2")},
		{EngineVersion: aws.String("17.2"), DBParameterGroupName: aws.String("my16")},
	} {
		in.DBInstanceIdentifier = aws.String("fam1")
		in.AllowMajorVersionUpgrade = aws.Bool(true)
		in.ApplyImmediately = aws.Bool(true)

		_, err := client.ModifyDBInstance(ctx, in)

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterCombination" {
			t.Fatalf("ModifyDBInstance(%s, %s) err = %v, want InvalidParameterCombination",
				aws.ToString(in.EngineVersion), aws.ToString(in.DBParameterGroupName), err)
		}
	}

	mod, err := client.ModifyDBInstance(ctx, &awsrds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String("fam1"), EngineVersion: aws.String("16.2"),
		DBParameterGroupName: aws.String("my16"), AllowMajorVersionUpgrade: aws.Bool(true),
		ApplyImmediately: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ModifyDBInstance to 16.2 with my16: %v", err)
	}

	if g := aws.ToString(mod.DBInstance.DBParameterGroups[0].DBParameterGroupName); g != "my16" {
		t.Fatalf("parameter group = %q, want my16", g)
	}
}

// TestSDKRDSAuroraMajorUpgradeMovesMembers: modify-db-cluster with a major
// version moves every member instance to the new version and default group,
// or to DBInstanceParameterGroupName when given.
func TestSDKRDSAuroraMajorUpgradeMovesMembers(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("ac1"), Engine: aws.String("aurora-postgresql"),
		EngineVersion: aws.String("15.4"), MasterUsername: aws.String("admin"),
		MasterUserPassword: aws.String("secret123"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	for _, id := range []string{"ac1w", "ac1r"} {
		if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String(id), DBClusterIdentifier: aws.String("ac1"),
			Engine: aws.String("aurora-postgresql"), DBInstanceClass: aws.String("db.r5.large"),
		}); err != nil {
			t.Fatalf("CreateDBInstance %s: %v", id, err)
		}
	}

	if _, err := client.ModifyDBCluster(ctx, &awsrds.ModifyDBClusterInput{
		DBClusterIdentifier: aws.String("ac1"), EngineVersion: aws.String("16.2"),
		AllowMajorVersionUpgrade: aws.Bool(true), ApplyImmediately: aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBCluster: %v", err)
	}

	out, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	for i := range out.DBInstances {
		inst := out.DBInstances[i]
		g := aws.ToString(inst.DBParameterGroups[0].DBParameterGroupName)

		if aws.ToString(inst.EngineVersion) != "16.2" || g != "default.aurora-postgresql16" {
			t.Fatalf("member %s = %s / %s, want 16.2 / default.aurora-postgresql16",
				aws.ToString(inst.DBInstanceIdentifier), aws.ToString(inst.EngineVersion), g)
		}
	}

	if _, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
		DBParameterGroupName: aws.String("apg17"), DBParameterGroupFamily: aws.String("aurora-postgresql17"),
		Description: aws.String("apg17"),
	}); err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if _, err := client.ModifyDBCluster(ctx, &awsrds.ModifyDBClusterInput{
		DBClusterIdentifier: aws.String("ac1"), EngineVersion: aws.String("17.2"),
		AllowMajorVersionUpgrade: aws.Bool(true), ApplyImmediately: aws.Bool(true),
		DBInstanceParameterGroupName: aws.String("apg17"),
	}); err != nil {
		t.Fatalf("ModifyDBCluster with DBInstanceParameterGroupName: %v", err)
	}

	got, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String("ac1r")})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if g := aws.ToString(got.DBInstances[0].DBParameterGroups[0].DBParameterGroupName); g != "apg17" {
		t.Fatalf("member group = %q, want apg17", g)
	}
}

// TestSDKRDSUnknownDefaultGroupNotFound: a "default." name of no real family
// is DBParameterGroupNotFound, not an immutable default group.
func TestSDKRDSUnknownDefaultGroupNotFound(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.ModifyDBParameterGroup(ctx, &awsrds.ModifyDBParameterGroupInput{
		DBParameterGroupName: aws.String("default.bogus"),
		Parameters: []rdstypes.Parameter{{
			ParameterName: aws.String("max_connections"), ParameterValue: aws.String("10"),
			ApplyMethod: rdstypes.ApplyMethodImmediate,
		}},
	})

	var notFound *rdstypes.DBParameterGroupNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("ModifyDBParameterGroup(default.bogus) err = %v, want DBParameterGroupNotFound", err)
	}

	_, err = client.DescribeDBParameterGroups(ctx, &awsrds.DescribeDBParameterGroupsInput{
		DBParameterGroupName: aws.String("default.bogus"),
	})
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeDBParameterGroups(default.bogus) err = %v, want DBParameterGroupNotFound", err)
	}
}
