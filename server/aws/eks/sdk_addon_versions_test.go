package eks_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKDescribeAddonVersions calls GET /addons/supported-versions through
// the real SDK. S3 is registered too, so this also proves the path no
// longer falls through to the S3 catch-all.
func TestSDKDescribeAddonVersions(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.DescribeAddonVersions(ctx, &awseks.DescribeAddonVersionsInput{
		AddonName:         aws.String("vpc-cni"),
		KubernetesVersion: aws.String("1.30"),
	})
	if err != nil {
		t.Fatalf("DescribeAddonVersions: %v", err)
	}

	if len(out.Addons) != 1 || aws.ToString(out.Addons[0].AddonName) != "vpc-cni" {
		t.Fatalf("addons = %+v", out.Addons)
	}

	addon := out.Addons[0]
	if aws.ToString(addon.Type) != "networking" || aws.ToString(addon.Owner) != "aws" || aws.ToString(addon.Publisher) != "eks" {
		t.Fatalf("addon metadata = %+v", addon)
	}

	defaults := 0

	for _, v := range addon.AddonVersions {
		for _, c := range v.Compatibilities {
			if aws.ToString(c.ClusterVersion) != "1.30" {
				t.Fatalf("compatibility for %s leaked into a 1.30 query", aws.ToString(c.ClusterVersion))
			}

			if c.DefaultVersion {
				defaults++
			}
		}
	}

	if defaults != 1 {
		t.Fatalf("got %d default versions for 1.30, want 1", defaults)
	}

	none, err := client.DescribeAddonVersions(ctx, &awseks.DescribeAddonVersionsInput{AddonName: aws.String("not-an-addon")})
	if err != nil || len(none.Addons) != 0 {
		t.Fatalf("unknown add-on: %+v err %v", none, err)
	}

	count := 0

	pager := awseks.NewDescribeAddonVersionsPaginator(client, &awseks.DescribeAddonVersionsInput{MaxResults: aws.Int32(4)})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("page: %v", err)
		}

		count += len(page.Addons)
	}

	if count != 12 {
		t.Fatalf("paged %d add-ons, want 12", count)
	}
}

func TestSDKDescribeAddonConfiguration(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.DescribeAddonConfiguration(ctx, &awseks.DescribeAddonConfigurationInput{
		AddonName: aws.String("aws-ebs-csi-driver"), AddonVersion: aws.String("v1.45.0-eksbuild.2"),
	})
	if err != nil {
		t.Fatalf("DescribeAddonConfiguration: %v", err)
	}

	if !json.Valid([]byte(aws.ToString(out.ConfigurationSchema))) {
		t.Fatalf("schema is not JSON: %s", aws.ToString(out.ConfigurationSchema))
	}

	if len(out.PodIdentityConfiguration) != 1 ||
		aws.ToString(out.PodIdentityConfiguration[0].ServiceAccount) != "ebs-csi-controller-sa" {
		t.Fatalf("pod identity = %+v", out.PodIdentityConfiguration)
	}

	_, err = client.DescribeAddonConfiguration(ctx, &awseks.DescribeAddonConfigurationInput{
		AddonName: aws.String("aws-ebs-csi-driver"), AddonVersion: aws.String("v0.0.0"),
	})

	var invalid *ekstypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("unknown version: want InvalidParameterException, got %v", err)
	}
}
