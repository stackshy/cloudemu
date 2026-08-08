// Package aws provides AWS mock provider factories.
package aws

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecr"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	"github.com/stackshy/cloudemu/v2/providers/aws/eks"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/providers/aws/elbv2"
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	"github.com/stackshy/cloudemu/v2/providers/aws/iam"
	"github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	"github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	"github.com/stackshy/cloudemu/v2/providers/aws/memorydb"
	"github.com/stackshy/cloudemu/v2/providers/aws/networkfirewall"
	"github.com/stackshy/cloudemu/v2/providers/aws/rds"
	"github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53resolver"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/providers/aws/sagemaker"
	"github.com/stackshy/cloudemu/v2/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/v2/providers/aws/sns"
	"github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpclattice"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// eksClusters is the slice of the EKS mock that discovery reads — an interface
// so the vanished-cluster (NotFound) skip can be tested with a fake.
type eksClusters interface {
	ListClusters(ctx context.Context) ([]string, error)
	DescribeCluster(ctx context.Context, name string) (*eksdriver.Cluster, error)
	ListNodegroups(ctx context.Context, clusterName string) ([]string, error)
}

// eksDiscovery adapts the EKS mock to the resourcediscovery KubernetesClusters
// capability, so EKS clusters and their node groups surface in Resource
// Explorer. Kept in the provider package (not services/) to avoid inverting
// the layering — the discovery engine stays free of provider imports.
type eksDiscovery struct{ m eksClusters }

func (a eksDiscovery) DiscoverClusters(ctx context.Context) ([]resourcediscovery.DiscoveredCluster, error) {
	names, err := a.m.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredCluster, 0, len(names))

	for _, name := range names {
		// Discovery is a shared, polled surface, so a DeleteCluster can race
		// between ListClusters and these per-cluster reads. A vanished cluster
		// (NotFound) is correct to omit — skip it rather than fail the whole
		// walk, which engine.List would propagate into every provider's
		// inventory. Any other error is real and propagates.
		c, err := a.m.DescribeCluster(ctx, name)
		if cerrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return nil, err
		}

		ngs, err := a.m.ListNodegroups(ctx, name)
		if cerrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return nil, err
		}

		dc := resourcediscovery.DiscoveredCluster{Name: name, NodeGroups: resourcediscovery.NodeGroupsFromNames(ngs)}
		if c != nil {
			dc.ARN = c.ARN // use the EKS mock's own ARN verbatim
			// Keep Region in step with the verbatim ARN so the node-group ARN
			// (built from Region) and Resource.Region can't diverge from it.
			dc.Region = eksRegionFromARN(c.ARN)
			dc.Tags = c.Tags
		}

		out = append(out, dc)
	}

	return out, nil
}

// eksRegionFromARN pulls the region out of an EKS ARN
// (arn:aws:eks:<region>:<account>:cluster/<name>). Returns "" if unparseable,
// which leaves the walker to fall back to the engine's default region.
func eksRegionFromARN(arn string) string {
	const regionField = 3

	parts := strings.Split(arn, ":")
	if len(parts) <= regionField {
		return ""
	}

	return parts[regionField]
}

// Provider holds all AWS mock services.
type Provider struct {
	S3                  *s3.Mock
	EC2                 *ec2.Mock
	DynamoDB            *dynamodb.Mock
	Lambda              *lambda.Mock
	VPC                 *vpc.Mock
	CloudWatch          *cloudwatch.Mock
	IAM                 *iam.Mock
	Route53             *route53.Mock
	ELB                 *elbv2.Mock
	SQS                 *sqs.Mock
	ElastiCache         *elasticache.Mock
	Keyspaces           *keyspaces.Mock
	MemoryDB            *memorydb.Mock
	NetworkFirewall     *networkfirewall.Mock
	SecretsManager      *secretsmanager.Mock
	CloudWatchLogs      *cloudwatchlogs.Mock
	SNS                 *sns.Mock
	ECR                 *ecr.Mock
	EventBridge         *eventbridge.Mock
	RDS                 *rds.Mock
	Redshift            *redshift.Mock
	EKS                 *eks.Mock
	Bedrock             *bedrock.Mock
	BedrockAgent        *bedrockagent.Mock
	BedrockAgentRuntime *bedrockagentruntime.Mock
	SageMaker           *sagemaker.Mock
	SSM                 *ssm.Mock
	ECS                 *ecs.Mock
	VPCLattice          *vpclattice.Mock
	Route53Resolver     *route53resolver.Mock
	ResourceDiscovery   *resourcediscovery.Engine
	AccountID           string
	Region              string
}

// New creates a new AWS provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		S3:                  s3.New(o),
		EC2:                 ec2.New(o),
		DynamoDB:            dynamodb.New(o),
		Lambda:              lambda.New(o),
		VPC:                 vpc.New(o),
		CloudWatch:          cloudwatch.New(o),
		IAM:                 iam.New(o),
		Route53:             route53.New(o),
		ELB:                 elbv2.New(o),
		SQS:                 sqs.New(o),
		ElastiCache:         elasticache.New(o),
		Keyspaces:           keyspaces.New(o),
		MemoryDB:            memorydb.New(o),
		NetworkFirewall:     networkfirewall.New(o),
		SecretsManager:      secretsmanager.New(o),
		CloudWatchLogs:      cloudwatchlogs.New(o),
		SNS:                 sns.New(o),
		ECR:                 ecr.New(o),
		EventBridge:         eventbridge.New(o),
		RDS:                 rds.New(o),
		Redshift:            redshift.New(o),
		EKS:                 eks.New(o),
		Bedrock:             bedrock.New(o),
		BedrockAgent:        bedrockagent.New(o),
		BedrockAgentRuntime: bedrockagentruntime.New(o),
		SageMaker:           sagemaker.New(o),
		SSM:                 ssm.New(o),
		ECS:                 ecs.New(o),
		VPCLattice:          vpclattice.New(o),
		Route53Resolver:     route53resolver.New(o),
		AccountID:           o.AccountID,
		Region:              o.Region,
	}
	p.EC2.SetMonitoring(p.CloudWatch)
	p.S3.SetMonitoring(p.CloudWatch)
	p.DynamoDB.SetMonitoring(p.CloudWatch)
	p.Lambda.SetMonitoring(p.CloudWatch)
	p.SQS.SetMonitoring(p.CloudWatch)
	p.ElastiCache.SetMonitoring(p.CloudWatch)
	p.MemoryDB.SetMonitoring(p.CloudWatch)
	p.CloudWatchLogs.SetMonitoring(p.CloudWatch)
	p.SNS.SetMonitoring(p.CloudWatch)
	p.ECR.SetMonitoring(p.CloudWatch)
	p.EventBridge.SetMonitoring(p.CloudWatch)
	p.RDS.SetMonitoring(p.CloudWatch)
	p.RDS.SetSubnetResolver(p.VPC)
	p.ElastiCache.SetSubnetResolver(p.VPC)
	p.EC2.SetSubnetResolver(p.VPC)
	p.SSM.SetInstanceResolver(p.EC2)
	// ECS-registered container instances surface as managed EC2 instances, so
	// #159 (ECS) composes with #300 (EC2 managed-resource visibility).
	p.ECS.SetManagedInstanceLauncher(p.EC2)
	p.Redshift.SetMonitoring(p.CloudWatch)
	p.EKS.SetMonitoring(p.CloudWatch)
	p.SageMaker.SetMonitoring(p.CloudWatch)
	// SNS -> SQS fan-out: publishes deliver to SQS-protocol subscriptions.
	p.SNS.SetSQSDeliverer(p.SQS)
	// EventBridge -> SQS: matched rules deliver events to SQS targets.
	p.EventBridge.SetSQSDeliverer(p.SQS)
	// S3 -> SQS: object-create events deliver to bucket notification targets.
	p.S3.SetSQSDeliverer(p.SQS)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAWS, o.AccountID, o.Region,
		&resourcediscovery.Drivers{
			Compute:      p.EC2,
			Networking:   p.VPC,
			Storage:      p.S3,
			Database:     p.DynamoDB,
			Serverless:   p.Lambda,
			Kubernetes:   eksDiscovery{p.EKS},
			RelationalDB: rdsDiscovery{p.RDS},
			Secrets:      p.SecretsManager,
			ContainerReg: p.ECR,
			MessageQueue: p.SQS,
			Notification: p.SNS,
			DNS:          p.Route53,
			Logging:      p.CloudWatchLogs,
			Cache:        p.ElastiCache,
			LoadBalancer: p.ELB,
			Monitoring:   p.CloudWatch,
			IAM:          p.IAM,
		},
	)

	return p
}
