package rds_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newSubnetGroupClients returns RDS and EC2 clients sharing one emulator, so
// subnets created through EC2 are the same ones RDS resolves a VPC from.
func newSubnetGroupClients(t *testing.T) (*awsrds.Client, *awsec2.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.DriversFrom(cloud))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awsrds.NewFromConfig(cfg), awsec2.NewFromConfig(cfg)
}

func mkSubnets(t *testing.T, ec2c *awsec2.Client) (vpcID string, subnetIDs []string) {
	t.Helper()
	ctx := context.Background()

	vpc, err := ec2c.CreateVpc(ctx, &awsec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID = aws.ToString(vpc.Vpc.VpcId)

	for _, cidr := range []string{"10.0.1.0/24", "10.0.2.0/24"} {
		sub, err := ec2c.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
			VpcId: aws.String(vpcID), CidrBlock: aws.String(cidr),
		})
		if err != nil {
			t.Fatalf("CreateSubnet(%s): %v", cidr, err)
		}

		subnetIDs = append(subnetIDs, aws.ToString(sub.Subnet.SubnetId))
	}

	return vpcID, subnetIDs
}

// The real SDK encodes SubnetIds as SubnetIds.SubnetIdentifier.N. Driving this
// through the actual client is the only way to prove the server reads the same
// shape the client writes — a hand-rolled form would just re-assert my guess.
func TestSubnetGroupSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	vpcID, subnetIDs := mkSubnets(t, ec2c)

	created, err := rdsc.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("db-sng-1"),
		DBSubnetGroupDescription: aws.String("test group"),
		SubnetIds:                subnetIDs,
	})
	if err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	if got := aws.ToString(created.DBSubnetGroup.DBSubnetGroupName); got != "db-sng-1" {
		t.Errorf("name = %q, want db-sng-1", got)
	}

	// VpcId is derived from the member subnets, never supplied by the caller.
	// A VPC teardown lists groups and matches on exactly this field, so an
	// empty value here means subnet groups leak on every delete.
	if got := aws.ToString(created.DBSubnetGroup.VpcId); got != vpcID {
		t.Errorf("VpcId = %q, want %q", got, vpcID)
	}

	if n := len(created.DBSubnetGroup.Subnets); n != 2 {
		t.Errorf("subnets = %d, want 2", n)
	}

	listed, err := rdsc.DescribeDBSubnetGroups(ctx, &awsrds.DescribeDBSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBSubnetGroups: %v", err)
	}

	if len(listed.DBSubnetGroups) != 1 {
		t.Fatalf("describe = %d groups, want 1", len(listed.DBSubnetGroups))
	}

	if got := aws.ToString(listed.DBSubnetGroups[0].VpcId); got != vpcID {
		t.Errorf("listed VpcId = %q, want %q", got, vpcID)
	}

	if _, err := rdsc.DeleteDBSubnetGroup(ctx, &awsrds.DeleteDBSubnetGroupInput{
		DBSubnetGroupName: aws.String("db-sng-1"),
	}); err != nil {
		t.Fatalf("DeleteDBSubnetGroup: %v", err)
	}

	after, err := rdsc.DescribeDBSubnetGroups(ctx, &awsrds.DescribeDBSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBSubnetGroups after delete: %v", err)
	}

	if len(after.DBSubnetGroups) != 0 {
		t.Errorf("group survived delete: %+v", after.DBSubnetGroups)
	}
}

// Callers re-running a provision treat DBSubnetGroupAlreadyExists as "already
// there, carry on". If the duplicate surfaced as a generic error the re-run
// would fail outright, so the code has to be present in the message.
func TestCreateDuplicateSubnetGroupReportsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	_, subnetIDs := mkSubnets(t, ec2c)

	in := &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("dup-sng"),
		DBSubnetGroupDescription: aws.String("first"),
		SubnetIds:                subnetIDs,
	}

	if _, err := rdsc.CreateDBSubnetGroup(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := rdsc.CreateDBSubnetGroup(ctx, in)
	if err == nil {
		t.Fatal("duplicate create should fail")
	}

	if !strings.Contains(err.Error(), "DBSubnetGroupAlreadyExists") {
		t.Errorf("error must name DBSubnetGroupAlreadyExists, got: %v", err)
	}
}

func TestDeleteUnknownSubnetGroupFails(t *testing.T) {
	rdsc, _ := newSubnetGroupClients(t)

	_, err := rdsc.DeleteDBSubnetGroup(context.Background(), &awsrds.DeleteDBSubnetGroupInput{
		DBSubnetGroupName: aws.String("nope"),
	})
	if err == nil {
		t.Error("deleting an unknown subnet group should fail")
	}
}
