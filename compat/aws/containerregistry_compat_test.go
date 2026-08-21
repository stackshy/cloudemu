package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	crService  = "containerregistry"
	crRepoName = "compat-repo"
	crImageTag = "v1"
	crManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
)

// TestContainerRegistryAWSCompat drives a real aws-sdk-go-v2 ECR client against
// CloudEmu's in-process wire server and records one compat result per portable
// containerregistry op the AWS handler routes.
func TestContainerRegistryAWSCompat(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{ECR: cloud.ECR})

	client := awsecr.NewFromConfig(sess.Config(), func(o *awsecr.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	ctx := context.Background()

	sess.Op(crService, "CreateRepository", func() error {
		out, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
			RepositoryName: aws.String(crRepoName),
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.Repository.RepositoryName) != crRepoName {
			return fmt.Errorf("CreateRepository name = %q", aws.ToString(out.Repository.RepositoryName))
		}

		return nil
	})

	sess.Op(crService, "ListRepositories", func() error {
		out, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{})
		if err != nil {
			return err
		}

		if len(out.Repositories) != 1 {
			return fmt.Errorf("ListRepositories returned %d repositories, want 1", len(out.Repositories))
		}

		return nil
	})

	sess.Op(crService, "GetRepository", func() error {
		out, err := client.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
			RepositoryNames: []string{crRepoName},
		})
		if err != nil {
			return err
		}

		if len(out.Repositories) != 1 || aws.ToString(out.Repositories[0].RepositoryName) != crRepoName {
			return fmt.Errorf("GetRepository returned %+v", out.Repositories)
		}

		return nil
	})

	sess.Op(crService, "PutImage", func() error {
		out, err := client.PutImage(ctx, &awsecr.PutImageInput{
			RepositoryName: aws.String(crRepoName),
			ImageManifest:  aws.String(crManifest),
			ImageTag:       aws.String(crImageTag),
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.Image.ImageId.ImageTag) != crImageTag {
			return fmt.Errorf("PutImage tag = %q", aws.ToString(out.Image.ImageId.ImageTag))
		}

		return nil
	})

	sess.Op(crService, "ListImages", func() error {
		out, err := client.ListImages(ctx, &awsecr.ListImagesInput{
			RepositoryName: aws.String(crRepoName),
		})
		if err != nil {
			return err
		}

		if len(out.ImageIds) != 1 || aws.ToString(out.ImageIds[0].ImageTag) != crImageTag {
			return fmt.Errorf("ListImages returned %+v", out.ImageIds)
		}

		return nil
	})

	sess.Op(crService, "GetImage", func() error {
		out, err := client.DescribeImages(ctx, &awsecr.DescribeImagesInput{
			RepositoryName: aws.String(crRepoName),
		})
		if err != nil {
			return err
		}

		if len(out.ImageDetails) != 1 {
			return fmt.Errorf("GetImage returned %d details, want 1", len(out.ImageDetails))
		}

		return nil
	})

	sess.Op(crService, "DeleteImage", func() error {
		out, err := client.BatchDeleteImage(ctx, &awsecr.BatchDeleteImageInput{
			RepositoryName: aws.String(crRepoName),
			ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String(crImageTag)}},
		})
		if err != nil {
			return err
		}

		if len(out.ImageIds) != 1 || len(out.Failures) != 0 {
			return fmt.Errorf("DeleteImage ids=%+v failures=%+v", out.ImageIds, out.Failures)
		}

		return nil
	})

	sess.Op(crService, "DeleteRepository", func() error {
		_, err := client.DeleteRepository(ctx, &awsecr.DeleteRepositoryInput{
			RepositoryName: aws.String(crRepoName),
			Force:          true,
		})

		return err
	})
}
