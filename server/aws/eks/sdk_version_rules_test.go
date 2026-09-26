package eks_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// requireInvalidParameter fails unless err is an InvalidParameterException
// whose message contains want.
func requireInvalidParameter(t *testing.T, err error, want string) {
	t.Helper()

	var ipe *ekstypes.InvalidParameterException
	if !errors.As(err, &ipe) {
		t.Fatalf("expected InvalidParameterException, got %T: %v", err, err)
	}

	if !strings.Contains(aws.ToString(ipe.Message), want) {
		t.Fatalf("message %q does not contain %q", aws.ToString(ipe.Message), want)
	}
}

func createVersionCluster(t *testing.T, client *awseks.Client, version string) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awseks.CreateClusterInput{
		Name:               aws.String("v1"),
		Version:            aws.String(version),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

func createVersionNodegroup(t *testing.T, client *awseks.Client, name, version string) error {
	t.Helper()

	in := &awseks.CreateNodegroupInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String(name),
		NodeRole: aws.String("arn:aws:iam::123456789012:role/node"), Subnets: []string{"subnet-1"},
	}
	if version != "" {
		in.Version = aws.String(version)
	}

	_, err := client.CreateNodegroup(context.Background(), in)

	return err
}

func TestSDKEKSClusterVersionRules(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, v := range []string{"0.1", "1.30"} {
		_, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
			Name:               aws.String("bad"),
			Version:            aws.String(v),
			RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		})
		requireInvalidParameter(t, err, "unsupported Kubernetes version "+v)
	}

	createVersionCluster(t, client, "1.32")

	_, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.34"),
	})
	requireInvalidParameter(t, err, "Unsupported Kubernetes minor version update from 1.32 to 1.34")

	if _, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.33"),
	}); err != nil {
		t.Fatalf("UpdateClusterVersion 1.33: %v", err)
	}

	if err := createVersionNodegroup(t, client, "ng1", ""); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	// The nodegroup runs 1.33, so a plain rollback is refused.
	_, err = client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.32"),
	})

	var ise *ekstypes.InvalidStateException
	if !errors.As(err, &ise) {
		t.Fatalf("expected InvalidStateException, got %T: %v", err, err)
	}

	upd, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.32"), Force: true,
	})
	if err != nil {
		t.Fatalf("forced rollback: %v", err)
	}

	if upd.Update.Type != "VersionRollback" {
		t.Fatalf("update type = %q, want VersionRollback", upd.Update.Type)
	}
}

func TestSDKEKSUpgradeNeedsNodegroupsAtClusterVersion(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createVersionCluster(t, client, "1.33")

	if err := createVersionNodegroup(t, client, "old", "1.32"); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	_, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.34"), Force: true,
	})

	var ire *ekstypes.InvalidRequestException
	if !errors.As(err, &ire) || !strings.Contains(aws.ToString(ire.Message), "old") {
		t.Fatalf("expected InvalidRequestException naming the nodegroup, got %T: %v", err, err)
	}
}

func TestSDKEKSNodegroupVersionCap(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createVersionCluster(t, client, "1.31")

	err := createVersionNodegroup(t, client, "ng1", "1.32")
	requireInvalidParameter(t, err, "cannot be newer than cluster v1 version 1.31")

	if err := createVersionNodegroup(t, client, "ng1", ""); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if aws.ToString(got.Nodegroup.Version) != "1.31" {
		t.Fatalf("nodegroup version = %q, want the cluster version 1.31", aws.ToString(got.Nodegroup.Version))
	}

	_, err = client.UpdateNodegroupVersion(ctx, &awseks.UpdateNodegroupVersionInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"), Version: aws.String("1.32"),
	})
	requireInvalidParameter(t, err, "cannot be newer than cluster v1 version 1.31")
}

func TestSDKEKSAddonVersionResolution(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createVersionCluster(t, client, "1.32")

	if _, err := client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("vpc-cni"),
	}); err != nil {
		t.Fatalf("CreateAddon: %v", err)
	}

	got, err := client.DescribeAddon(ctx, &awseks.DescribeAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("vpc-cni"),
	})
	if err != nil {
		t.Fatalf("DescribeAddon: %v", err)
	}

	if v := aws.ToString(got.Addon.AddonVersion); v != "v1.23.1-eksbuild.1" {
		t.Fatalf("addonVersion = %q, want the 1.32 default v1.23.1-eksbuild.1", v)
	}

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("coredns"), AddonVersion: aws.String("v0.0.1"),
	})
	requireInvalidParameter(t, err, "Addon version specified is not supported")

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("made-up"),
	})
	requireInvalidParameter(t, err, "Addon made-up specified is not supported in 1.32 kubernetes version")

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("coredns"), ConfigurationValues: aws.String("{broken"),
	})
	requireInvalidParameter(t, err, "ConfigurationValue provided in request is not supported")
}

// TestEKSUpdateClusterVersionRollbackConfig sends rollbackConfig on the wire.
// SDK v1.83 has no field for it, so the request is raw JSON.
func TestEKSUpdateClusterVersionRollbackConfig(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{EKS: cloud.EKS}))
	t.Cleanup(ts.Close)

	create := `{"name":"v1","version":"1.32","roleArn":"arn:aws:iam::123456789012:role/eks",` +
		`"resourcesVpcConfig":{"subnetIds":["subnet-1"]}}`
	if code, body := postEKS(t, ts.URL+"/clusters", create); code != http.StatusOK {
		t.Fatalf("CreateCluster: %d %s", code, body)
	}

	code, body := postEKS(t, ts.URL+"/clusters/v1/updates", `{"version":"1.33","rollbackConfig":{"timeoutMinutes":60}}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "InvalidParameterException") ||
		!strings.Contains(body, "timeoutMinutes must be between 120 and 10080") {
		t.Fatalf("short timeout: %d %s", code, body)
	}

	code, body = postEKS(t, ts.URL+"/clusters/v1/updates", `{"version":"1.33","rollbackConfig":{"timeoutMinutes":720}}`)
	if code != http.StatusOK {
		t.Fatalf("valid timeout: %d %s", code, body)
	}
}

func postEKS(t *testing.T, url, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)

	return resp.StatusCode, string(raw) + " " + resp.Header.Get("X-Amzn-ErrorType")
}
