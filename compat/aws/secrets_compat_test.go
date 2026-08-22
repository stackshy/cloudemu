package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSSecretsCompat drives a Secrets Manager secret lifecycle through the
// real aws-sdk-go-v2 client. Operation names match the portable "secrets"
// driver in docs/coverage/coverage.json: the DescribeSecret SDK call maps to
// the "GetSecret" driver op, and ListSecretVersionIds maps to
// "ListSecretVersions".
func TestAWSSecretsCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{SecretsManager: provider.SecretsManager})

	client := awssm.NewFromConfig(sess.Config(), func(o *awssm.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc    = "secrets"
		name   = "db-password"
		value1 = "hunter2"
		value2 = "hunter3"
	)

	sess.Op(svc, "CreateSecret", func() error {
		out, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name:         aws.String(name),
			Description:  aws.String("primary database password"),
			SecretString: aws.String(value1),
			Tags:         []smtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
		})
		if err != nil {
			return err
		}

		if aws.ToString(out.Name) != name || aws.ToString(out.ARN) == "" {
			return fmt.Errorf("CreateSecret returned name=%q arn=%q", aws.ToString(out.Name), aws.ToString(out.ARN))
		}

		return nil
	})

	sess.Op(svc, "GetSecret", func() error {
		out, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String(name)})
		if err != nil {
			return err
		}

		if aws.ToString(out.Description) != "primary database password" {
			return fmt.Errorf("DescribeSecret description = %q", aws.ToString(out.Description))
		}

		return nil
	})

	sess.Op(svc, "ListSecrets", func() error {
		out, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{})
		if err != nil {
			return err
		}

		for _, s := range out.SecretList {
			if aws.ToString(s.Name) == name {
				return nil
			}
		}

		return fmt.Errorf("secret %q not found in ListSecrets", name)
	})

	sess.Op(svc, "GetSecretValue", func() error {
		out, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String(name)})
		if err != nil {
			return err
		}

		if aws.ToString(out.SecretString) != value1 {
			return fmt.Errorf("GetSecretValue = %q, want %q", aws.ToString(out.SecretString), value1)
		}

		return nil
	})

	sess.Op(svc, "PutSecretValue", func() error {
		_, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
			SecretId:     aws.String(name),
			SecretString: aws.String(value2),
		})

		return err
	})

	sess.Op(svc, "ListSecretVersions", func() error {
		out, err := client.ListSecretVersionIds(ctx, &awssm.ListSecretVersionIdsInput{SecretId: aws.String(name)})
		if err != nil {
			return err
		}

		if len(out.Versions) < 2 {
			return fmt.Errorf("ListSecretVersionIds returned %d versions, want >= 2", len(out.Versions))
		}

		return nil
	})

	sess.Op(svc, "DeleteSecret", func() error {
		_, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String(name)})
		return err
	})
}
