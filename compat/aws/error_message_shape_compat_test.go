package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/vpclattice"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	waftypes "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	smithy "github.com/aws/smithy-go"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// codePrefixes are the internal cloudemu error-taxonomy tokens that
// cerrors.(*Error).Error() prepends as "<Code>: <Message>". A real AWS error
// MESSAGE never begins with one of these; only the machine-readable Code member
// carries the taxonomy. Every wire handler must render the human message via
// cerrors.Message(err), not err.Error().
var codePrefixes = []string{
	"NotFound: ",
	"AlreadyExists: ",
	"InvalidArgument: ",
	"FailedPrecondition: ",
	"PermissionDenied: ",
	"Throttled: ",
	"Internal: ",
	"Unimplemented: ",
	"ResourceExhausted: ",
}

// assertNoCodePrefix fails when the SDK-visible error message begins with an
// internal cloudemu code prefix. It requires a non-empty message so a dropped
// message (the CloudWatch CBOR bug) is caught too.
func assertNoCodePrefix(t *testing.T, service string, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected an error, got nil", service)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%s: error is not a smithy.APIError: %v", service, err)
	}

	msg := apiErr.ErrorMessage()
	if msg == "" {
		t.Fatalf("%s: error message is empty (code=%s)", service, apiErr.ErrorCode())
	}

	for _, p := range codePrefixes {
		if strings.HasPrefix(msg, p) {
			t.Fatalf("%s: error message leaks internal code prefix %q: %q (code=%s)",
				service, p, msg, apiErr.ErrorCode())
		}
	}
}

// TestAWSErrorMessageShapeCompat drives a not-found/duplicate error through the
// real aws-sdk-go-v2 client for every service that previously rendered the wire
// error message with err.Error() (leaking the internal "<Code>: " prefix). Each
// sub-test asserts the SDK-visible ErrorMessage() carries the clean human
// message only.
func TestAWSErrorMessageShapeCompat(t *testing.T) {
	sess := compat.BootAWS(t, awsserver.DriversFrom(cloudemu.NewAWS()))
	cfg := sess.Config()
	ep := sess.Endpoint()
	ctx := context.Background()

	t.Run("EFS", func(t *testing.T) {
		c := efs.NewFromConfig(cfg, func(o *efs.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeMountTargets(ctx, &efs.DescribeMountTargetsInput{
			FileSystemId: aws.String("fs-00000000"),
		})
		assertNoCodePrefix(t, "EFS.DescribeMountTargets", err)
	})

	t.Run("SFN", func(t *testing.T) {
		c := sfn.NewFromConfig(cfg, func(o *sfn.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
			ExecutionArn: aws.String("arn:aws:states:us-east-1:000000000000:execution:m:missing"),
		})
		assertNoCodePrefix(t, "SFN.DescribeExecution", err)
	})

	t.Run("SSM", func(t *testing.T) {
		c := ssm.NewFromConfig(cfg, func(o *ssm.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/missing")})
		assertNoCodePrefix(t, "SSM.GetParameter", err)
	})

	t.Run("Glue", func(t *testing.T) {
		c := glue.NewFromConfig(cfg, func(o *glue.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetDatabase(ctx, &glue.GetDatabaseInput{Name: aws.String("nope")})
		assertNoCodePrefix(t, "Glue.GetDatabase", err)
	})

	t.Run("KMS", func(t *testing.T) {
		c := kms.NewFromConfig(cfg, func(o *kms.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String("nope")})
		assertNoCodePrefix(t, "KMS.DescribeKey", err)
	})

	t.Run("Kinesis", func(t *testing.T) {
		c := kinesis.NewFromConfig(cfg, func(o *kinesis.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeStream(ctx, &kinesis.DescribeStreamInput{StreamName: aws.String("nope")})
		assertNoCodePrefix(t, "Kinesis.DescribeStream", err)
	})

	t.Run("ECR", func(t *testing.T) {
		c := ecr.NewFromConfig(cfg, func(o *ecr.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeImages(ctx, &ecr.DescribeImagesInput{RepositoryName: aws.String("nope")})
		assertNoCodePrefix(t, "ECR.DescribeImages", err)
	})

	t.Run("SecretsManager", func(t *testing.T) {
		c := secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String("nope")})
		assertNoCodePrefix(t, "SecretsManager.DescribeSecret", err)
	})

	t.Run("ElastiCache", func(t *testing.T) {
		c := elasticache.NewFromConfig(cfg, func(o *elasticache.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			CacheClusterId: aws.String("nope"),
		})
		assertNoCodePrefix(t, "ElastiCache.DescribeCacheClusters", err)
	})

	t.Run("Redshift", func(t *testing.T) {
		c := redshift.NewFromConfig(cfg, func(o *redshift.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeClusters(ctx, &redshift.DescribeClustersInput{
			ClusterIdentifier: aws.String("nope"),
		})
		assertNoCodePrefix(t, "Redshift.DescribeClusters", err)
	})

	t.Run("ACM", func(t *testing.T) {
		c := acm.NewFromConfig(cfg, func(o *acm.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
			CertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/nope"),
		})
		assertNoCodePrefix(t, "ACM.DescribeCertificate", err)
	})

	t.Run("OpenSearch", func(t *testing.T) {
		c := opensearch.NewFromConfig(cfg, func(o *opensearch.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeDomain(ctx, &opensearch.DescribeDomainInput{DomainName: aws.String("nope")})
		assertNoCodePrefix(t, "OpenSearch.DescribeDomain", err)
	})

	t.Run("WAFv2", func(t *testing.T) {
		c := wafv2.NewFromConfig(cfg, func(o *wafv2.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetIPSet(ctx, &wafv2.GetIPSetInput{
			Name:  aws.String("nope"),
			Scope: waftypes.ScopeRegional,
			Id:    aws.String("00000000-0000-0000-0000-000000000000"),
		})
		assertNoCodePrefix(t, "WAFv2.GetIPSet", err)
	})

	t.Run("CloudWatchLogs", func(t *testing.T) {
		c := cloudwatchlogs.NewFromConfig(cfg, func(o *cloudwatchlogs.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  aws.String("nope"),
			LogStreamName: aws.String("stream"),
		})
		assertNoCodePrefix(t, "CloudWatchLogs.GetLogEvents", err)
	})

	t.Run("Kafka", func(t *testing.T) {
		c := kafka.NewFromConfig(cfg, func(o *kafka.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeClusterV2(ctx, &kafka.DescribeClusterV2Input{
			ClusterArn: aws.String("arn:aws:kafka:us-east-1:000000000000:cluster/nope/00000000"),
		})
		assertNoCodePrefix(t, "Kafka.DescribeClusterV2", err)
	})

	t.Run("NetworkFirewall", func(t *testing.T) {
		c := networkfirewall.NewFromConfig(cfg, func(o *networkfirewall.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{
			FirewallName: aws.String("nope"),
		})
		assertNoCodePrefix(t, "NetworkFirewall.DescribeFirewall", err)
	})

	t.Run("CloudTrail", func(t *testing.T) {
		c := cloudtrail.NewFromConfig(cfg, func(o *cloudtrail.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetTrail(ctx, &cloudtrail.GetTrailInput{Name: aws.String("nope")})
		assertNoCodePrefix(t, "CloudTrail.GetTrail", err)
	})

	t.Run("GuardDuty", func(t *testing.T) {
		c := guardduty.NewFromConfig(cfg, func(o *guardduty.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetDetector(ctx, &guardduty.GetDetectorInput{DetectorId: aws.String("nope")})
		assertNoCodePrefix(t, "GuardDuty.GetDetector", err)
	})

	t.Run("VPCLattice", func(t *testing.T) {
		c := vpclattice.NewFromConfig(cfg, func(o *vpclattice.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetService(ctx, &vpclattice.GetServiceInput{ServiceIdentifier: aws.String("svc-nope")})
		assertNoCodePrefix(t, "VPCLattice.GetService", err)
	})

	t.Run("SNS", func(t *testing.T) {
		c := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(ep) })
		_, err := c.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{
			TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:nope"),
		})
		assertNoCodePrefix(t, "SNS.GetTopicAttributes", err)
	})
}

// TestAWSCloudWatchCBORErrorMessageCompat asserts that CloudWatch (the only
// rpc-v2-cbor service) surfaces the modeled error message to aws-sdk-go-v2.
// Before the fix, writeCBORError serialized the message under the CBOR key
// "Message" (capital) while the SDK reads "message" (lowercase), so every
// CloudWatch error arrived with an empty (or smithy-fallback "UnknownError")
// message.
func TestAWSCloudWatchCBORErrorMessageCompat(t *testing.T) {
	sess := compat.BootAWS(t, awsserver.DriversFrom(cloudemu.NewAWS()))
	c := cloudwatch.NewFromConfig(sess.Config(), func(o *cloudwatch.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	_, err := c.SetAlarmState(ctx, &cloudwatch.SetAlarmStateInput{
		AlarmName:   aws.String("ghost"),
		StateValue:  cwtypes.StateValueOk,
		StateReason: aws.String("test"),
	})
	if err == nil {
		t.Fatal("SetAlarmState on a missing alarm: expected an error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("SetAlarmState: error is not a smithy.APIError: %v", err)
	}

	msg := apiErr.ErrorMessage()
	if msg == "" || msg == "UnknownError" {
		t.Fatalf("SetAlarmState: CloudWatch CBOR error message was dropped (code=%s, message=%q)",
			apiErr.ErrorCode(), msg)
	}
}
