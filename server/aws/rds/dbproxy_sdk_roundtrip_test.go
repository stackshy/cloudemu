package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awsrdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

func TestSDKRDSProxyLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateDBProxy(ctx, &awsrds.CreateDBProxyInput{
		DBProxyName:  aws.String("px"),
		EngineFamily: awsrdstypes.EngineFamilyMysql,
		RoleArn:      aws.String("arn:aws:iam::123456789012:role/proxy"),
		VpcSubnetIds: []string{"subnet-a", "subnet-b"},
		Auth: []awsrdstypes.UserAuthConfig{
			{AuthScheme: awsrdstypes.AuthSchemeSecrets, SecretArn: aws.String("arn:secret"), IAMAuth: awsrdstypes.IAMAuthModeDisabled},
		},
	})
	if err != nil {
		t.Fatalf("CreateDBProxy: %v", err)
	}

	if aws.ToString(created.DBProxy.DBProxyArn) == "" {
		t.Error("expected proxy ARN")
	}

	if len(created.DBProxy.VpcSubnetIds) != 2 {
		t.Fatalf("subnet ids = %v, want 2", created.DBProxy.VpcSubnetIds)
	}

	desc, err := client.DescribeDBProxies(ctx, &awsrds.DescribeDBProxiesInput{})
	if err != nil {
		t.Fatalf("DescribeDBProxies: %v", err)
	}

	if len(desc.DBProxies) != 1 {
		t.Fatalf("got %d proxies, want 1", len(desc.DBProxies))
	}

	tls := true
	if _, err := client.ModifyDBProxy(ctx, &awsrds.ModifyDBProxyInput{
		DBProxyName: aws.String("px"),
		RequireTLS:  &tls,
	}); err != nil {
		t.Fatalf("ModifyDBProxy: %v", err)
	}

	// Register an instance target.
	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	reg, err := client.RegisterDBProxyTargets(ctx, &awsrds.RegisterDBProxyTargetsInput{
		DBProxyName:           aws.String("px"),
		DBInstanceIdentifiers: []string{"db"},
	})
	if err != nil {
		t.Fatalf("RegisterDBProxyTargets: %v", err)
	}

	if len(reg.DBProxyTargets) != 1 || aws.ToString(reg.DBProxyTargets[0].RdsResourceId) != "db" {
		t.Fatalf("unexpected registered targets: %+v", reg.DBProxyTargets)
	}

	targets, err := client.DescribeDBProxyTargets(ctx, &awsrds.DescribeDBProxyTargetsInput{
		DBProxyName: aws.String("px"),
	})
	if err != nil {
		t.Fatalf("DescribeDBProxyTargets: %v", err)
	}

	if len(targets.Targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets.Targets))
	}

	groups, err := client.DescribeDBProxyTargetGroups(ctx, &awsrds.DescribeDBProxyTargetGroupsInput{
		DBProxyName: aws.String("px"),
	})
	if err != nil {
		t.Fatalf("DescribeDBProxyTargetGroups: %v", err)
	}

	if len(groups.TargetGroups) != 1 || !aws.ToBool(groups.TargetGroups[0].IsDefault) {
		t.Fatalf("unexpected target groups: %+v", groups.TargetGroups)
	}

	if _, err := client.DeregisterDBProxyTargets(ctx, &awsrds.DeregisterDBProxyTargetsInput{
		DBProxyName:           aws.String("px"),
		DBInstanceIdentifiers: []string{"db"},
	}); err != nil {
		t.Fatalf("DeregisterDBProxyTargets: %v", err)
	}

	if _, err := client.DeleteDBProxy(ctx, &awsrds.DeleteDBProxyInput{
		DBProxyName: aws.String("px"),
	}); err != nil {
		t.Fatalf("DeleteDBProxy: %v", err)
	}
}
