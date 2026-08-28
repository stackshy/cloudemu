package aws

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// rgtCase is one service's end-to-end tag lifecycle through the Resource Groups
// Tagging API. create returns the ARN (or bare id, for Route 53) that RGT
// addresses; readTags reads the service's OWN tag store back through its real
// SDK; discoverable says whether the resource also surfaces in GetResources.
type rgtCase struct {
	name         string
	create       func(ctx context.Context) (string, error)
	readTags     func(ctx context.Context, arn string) (map[string]string, error)
	discoverable bool
}

// TestAWSRGTFullCoverageCompat drives newly-wired services through the real
// resourcegroupstaggingapi SDK: for each service it creates a resource with that
// service's own SDK, tags the ARN via TagResources, asserts the tag is visible
// via the service's own SDK tag read and (where the resource is discoverable)
// via GetResources with a TagFilter, then UntagResources and asserts it is gone
// from both. This exercises every generic-tagger shape: keyed-by-id (KMS),
// keyed-by-name (SSM), ARN passthrough (ECS/SFN), ARN-built discovery
// (Glue/GuardDuty), []Tag conversion (Keyspaces), alarm-name derivation
// (CloudWatch, tag-only), named methods (ACM), and the Route 53 non-ARN id.
func TestAWSRGTFullCoverageCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.DriversFrom(provider))
	ctx := context.Background()

	acct, region := provider.AccountID, provider.Region
	rgt := resourcegroupstaggingapi.NewFromConfig(sess.Config(), func(o *resourcegroupstaggingapi.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	for _, tc := range rgtCases(sess, acct, region) {
		t.Run(tc.name, func(t *testing.T) {
			arn, err := tc.create(ctx)
			if err != nil {
				t.Fatalf("%s create: %v", tc.name, err)
			}

			const key, val = "compat-team", "platform"

			tagResources(t, ctx, rgt, tc.name, arn, map[string]string{key: val})
			assertServiceTag(t, ctx, tc, arn, key, val)

			if tc.discoverable {
				assertDiscovered(t, ctx, rgt, tc.name, arn, key, val, true)
			}

			untagResources(t, ctx, rgt, tc.name, arn, []string{key})

			tags, err := tc.readTags(ctx, arn)
			if err != nil {
				t.Fatalf("%s read tags after untag: %v", tc.name, err)
			}

			if _, ok := tags[key]; ok {
				t.Fatalf("%s: tag %q still present after UntagResources", tc.name, key)
			}

			if tc.discoverable {
				assertDiscovered(t, ctx, rgt, tc.name, arn, key, val, false)
			}
		})
	}
}

func tagResources(t *testing.T, ctx context.Context, rgt *resourcegroupstaggingapi.Client,
	name, arn string, tags map[string]string,
) {
	t.Helper()

	out, err := rgt.TagResources(ctx, &resourcegroupstaggingapi.TagResourcesInput{
		ResourceARNList: []string{arn}, Tags: tags,
	})
	if err != nil {
		t.Fatalf("%s TagResources: %v", name, err)
	}

	if len(out.FailedResourcesMap) != 0 {
		t.Fatalf("%s TagResources failed for %s: %v", name, arn, out.FailedResourcesMap[arn])
	}
}

func untagResources(t *testing.T, ctx context.Context, rgt *resourcegroupstaggingapi.Client,
	name, arn string, keys []string,
) {
	t.Helper()

	out, err := rgt.UntagResources(ctx, &resourcegroupstaggingapi.UntagResourcesInput{
		ResourceARNList: []string{arn}, TagKeys: keys,
	})
	if err != nil {
		t.Fatalf("%s UntagResources: %v", name, err)
	}

	if len(out.FailedResourcesMap) != 0 {
		t.Fatalf("%s UntagResources failed for %s: %v", name, arn, out.FailedResourcesMap[arn])
	}
}

func assertServiceTag(t *testing.T, ctx context.Context, tc rgtCase, arn, key, val string) {
	t.Helper()

	tags, err := tc.readTags(ctx, arn)
	if err != nil {
		t.Fatalf("%s read own tags: %v", tc.name, err)
	}

	if got := tags[key]; got != val {
		t.Fatalf("%s own tag %q = %q, want %q (RGT tag did not land in the service store)", tc.name, key, got, val)
	}
}

// assertDiscovered checks GetResources with a TagFilter: want=true requires the
// ARN present with the tag; want=false requires it absent (after untag).
func assertDiscovered(t *testing.T, ctx context.Context, rgt *resourcegroupstaggingapi.Client,
	name, arn, key, val string, want bool,
) {
	t.Helper()

	out, err := rgt.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []rgttypes.TagFilter{{Key: aws.String(key), Values: []string{val}}},
	})
	if err != nil {
		t.Fatalf("%s GetResources: %v", name, err)
	}

	found := false
	for _, m := range out.ResourceTagMappingList {
		if aws.ToString(m.ResourceARN) == arn {
			found = true
			break
		}
	}

	if found != want {
		t.Fatalf("%s GetResources(tag %s=%s) found=%v, want %v (arn %s)", name, key, val, found, want, arn)
	}
}

func rgtCases(sess *compat.AWSSession, acct, region string) []rgtCase {
	cases := []rgtCase{
		kmsCase(sess), ssmCase(sess), ecsCase(sess), sfnCase(sess, acct),
		glueCase(sess, acct, region), keyspacesCase(sess), guarddutyCase(sess, acct, region),
		route53Case(sess), acmCase(sess), elbCase(sess),
	}

	return append(cases, cloudwatchCase(sess, acct, region))
}

func kmsCase(sess *compat.AWSSession) rgtCase {
	c := kms.NewFromConfig(sess.Config(), func(o *kms.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "kms",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateKey(ctx, &kms.CreateKeyInput{})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.KeyMetadata.Arn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListResourceTags(ctx, &kms.ListResourceTagsInput{KeyId: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.TagKey)] = aws.ToString(t.TagValue)
			}

			return tags, nil
		},
	}
}

func ssmCase(sess *compat.AWSSession) rgtCase {
	c := ssm.NewFromConfig(sess.Config(), func(o *ssm.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })
	const paramName = "compat-rgt-param"

	return rgtCase{
		name:         "ssm",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			_, err := c.PutParameter(ctx, &ssm.PutParameterInput{
				Name: aws.String(paramName), Value: aws.String("v"), Type: ssmtypes.ParameterTypeString,
			})
			if err != nil {
				return "", err
			}

			out, err := c.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(paramName)})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.Parameter.ARN), nil
		},
		readTags: func(ctx context.Context, _ string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &ssm.ListTagsForResourceInput{
				ResourceType: ssmtypes.ResourceTypeForTaggingParameter, ResourceId: aws.String(paramName),
			})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.TagList))
			for _, t := range out.TagList {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

func ecsCase(sess *compat.AWSSession) rgtCase {
	c := ecs.NewFromConfig(sess.Config(), func(o *ecs.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "ecs",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateCluster(ctx, &ecs.CreateClusterInput{ClusterName: aws.String("compat-rgt-ecs")})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.Cluster.ClusterArn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &ecs.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

func sfnCase(sess *compat.AWSSession, acct string) rgtCase {
	c := sfn.NewFromConfig(sess.Config(), func(o *sfn.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })
	const def = `{"StartAt":"a","States":{"a":{"Type":"Pass","End":true}}}`
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/compat-sfn", acct)

	return rgtCase{
		name:         "sfn",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
				Name: aws.String("compat-rgt-sfn"), Definition: aws.String(def), RoleArn: aws.String(roleARN),
			})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.StateMachineArn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &sfn.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

func glueCase(sess *compat.AWSSession, acct, region string) rgtCase {
	c := glue.NewFromConfig(sess.Config(), func(o *glue.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })
	const jobName = "compat-rgt-glue"
	arn := fmt.Sprintf("arn:aws:glue:%s:%s:job/%s", region, acct, jobName)

	return rgtCase{
		name:         "glue",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			_, err := c.CreateJob(ctx, &glue.CreateJobInput{
				Name: aws.String(jobName), Role: aws.String("compat-role"),
				Command: &gluetypes.JobCommand{Name: aws.String("glueetl")},
			})
			if err != nil {
				return "", err
			}

			return arn, nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.GetTags(ctx, &glue.GetTagsInput{ResourceArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			return out.Tags, nil
		},
	}
}

func keyspacesCase(sess *compat.AWSSession) rgtCase {
	c := keyspaces.NewFromConfig(sess.Config(), func(o *keyspaces.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "keyspaces",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateKeyspace(ctx, &keyspaces.CreateKeyspaceInput{KeyspaceName: aws.String("compat_rgt_ks")})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.ResourceArn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &keyspaces.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

func guarddutyCase(sess *compat.AWSSession, acct, region string) rgtCase {
	c := guardduty.NewFromConfig(sess.Config(), func(o *guardduty.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "guardduty",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateDetector(ctx, &guardduty.CreateDetectorInput{Enable: aws.Bool(true)})
			if err != nil {
				return "", err
			}

			return fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s", region, acct, aws.ToString(out.DetectorId)), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &guardduty.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			return out.Tags, nil
		},
	}
}

func route53Case(sess *compat.AWSSession) rgtCase {
	c := route53.NewFromConfig(sess.Config(), func(o *route53.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "route53",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
				Name: aws.String("compat-rgt.example."), CallerReference: aws.String("compat-rgt-ref"),
			})
			if err != nil {
				return "", err
			}

			// RGT addresses a hosted zone by its bare id; the CreateHostedZone
			// response gives "/hostedzone/Z…".
			return strings.TrimPrefix(aws.ToString(out.HostedZone.Id), "/hostedzone/"), nil
		},
		readTags: func(ctx context.Context, id string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &route53.ListTagsForResourceInput{
				ResourceType: r53types.TagResourceTypeHostedzone, ResourceId: aws.String(id),
			})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.ResourceTagSet.Tags))
			for _, t := range out.ResourceTagSet.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

func acmCase(sess *compat.AWSSession) rgtCase {
	c := acm.NewFromConfig(sess.Config(), func(o *acm.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "acm",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.RequestCertificate(ctx, &acm.RequestCertificateInput{DomainName: aws.String("compat-rgt.example.com")})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.CertificateArn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForCertificate(ctx, &acm.ListTagsForCertificateInput{CertificateArn: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}

// elbCase covers a service already discovered by an existing walker
// (walkLoadBalancer) whose tags flow through the new generic tagger's named
// AddResourceTags/RemoveResourceTags methods.
func elbCase(sess *compat.AWSSession) rgtCase {
	c := elb.NewFromConfig(sess.Config(), func(o *elb.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })

	return rgtCase{
		name:         "elbv2",
		discoverable: true,
		create: func(ctx context.Context) (string, error) {
			out, err := c.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
				Name: aws.String("compat-rgt-alb"), Type: elbtypes.LoadBalancerTypeEnumApplication,
				Subnets: []string{"subnet-a", "subnet-b"},
			})
			if err != nil {
				return "", err
			}

			return aws.ToString(out.LoadBalancers[0].LoadBalancerArn), nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.DescribeTags(ctx, &elb.DescribeTagsInput{ResourceArns: []string{arn}})
			if err != nil {
				return nil, err
			}

			tags := map[string]string{}
			for _, td := range out.TagDescriptions {
				for _, t := range td.Tags {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
			}

			return tags, nil
		},
	}
}

// cloudwatchCase is tag-only: CloudWatch alarms are not projected into
// GetResources here (walkMonitoring emits alarms without tags), so we assert the
// tag round-trip through the alarm's own ListTagsForResource only.
func cloudwatchCase(sess *compat.AWSSession, acct, region string) rgtCase {
	c := cloudwatch.NewFromConfig(sess.Config(), func(o *cloudwatch.Options) { o.BaseEndpoint = aws.String(sess.Endpoint()) })
	const alarmName = "compat-rgt-alarm"
	arn := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", region, acct, alarmName)

	return rgtCase{
		name:         "cloudwatch",
		discoverable: false,
		create: func(ctx context.Context) (string, error) {
			_, err := c.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
				AlarmName:          aws.String(alarmName),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				EvaluationPeriods:  aws.Int32(1),
				MetricName:         aws.String("CPUUtilization"),
				Namespace:          aws.String("AWS/EC2"),
				Period:             aws.Int32(60),
				Statistic:          cwtypes.StatisticAverage,
				Threshold:          aws.Float64(80),
			})
			if err != nil {
				return "", err
			}

			return arn, nil
		},
		readTags: func(ctx context.Context, arn string) (map[string]string, error) {
			out, err := c.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
			if err != nil {
				return nil, err
			}

			tags := make(map[string]string, len(out.Tags))
			for _, t := range out.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}

			return tags, nil
		},
	}
}
