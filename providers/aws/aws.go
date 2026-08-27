// Package aws provides AWS mock provider factories.
package aws

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/providers/aws/acm"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudtrail"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/configservice"
	"github.com/stackshy/cloudemu/v2/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecr"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	"github.com/stackshy/cloudemu/v2/providers/aws/efs"
	"github.com/stackshy/cloudemu/v2/providers/aws/eks"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/providers/aws/elbv2"
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	"github.com/stackshy/cloudemu/v2/providers/aws/glue"
	"github.com/stackshy/cloudemu/v2/providers/aws/guardduty"
	"github.com/stackshy/cloudemu/v2/providers/aws/iam"
	"github.com/stackshy/cloudemu/v2/providers/aws/kafka"
	"github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	"github.com/stackshy/cloudemu/v2/providers/aws/memorydb"
	"github.com/stackshy/cloudemu/v2/providers/aws/networkfirewall"
	"github.com/stackshy/cloudemu/v2/providers/aws/opensearch"
	"github.com/stackshy/cloudemu/v2/providers/aws/rds"
	"github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53resolver"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/providers/aws/sagemaker"
	"github.com/stackshy/cloudemu/v2/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/v2/providers/aws/sesv2"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	"github.com/stackshy/cloudemu/v2/providers/aws/sns"
	"github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpclattice"
	"github.com/stackshy/cloudemu/v2/providers/aws/wafv2"
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
	ACM                 *acm.Mock
	KMS                 *kms.Mock
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
	EFS                 *efs.Mock
	Kinesis             *kinesis.Mock
	SESV2               *sesv2.Mock
	OpenSearch          *opensearch.Mock
	Kafka               *kafka.Mock
	VPCLattice          *vpclattice.Mock
	WAFv2               *wafv2.Mock
	Route53Resolver     *route53resolver.Mock
	SFN                 *sfn.Mock
	CloudTrail          *cloudtrail.Mock
	Glue                *glue.Mock
	Config              *configservice.Mock
	GuardDuty           *guardduty.Mock
	ResourceDiscovery   *resourcediscovery.Engine
	AccountID           string
	Region              string

	// engineClosers holds any wired real engines that implement io.Closer, so
	// Close can cascade teardown to them. Empty for the in-memory default.
	engineClosers []io.Closer
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
		ACM:                 acm.New(o),
		KMS:                 kms.New(o),
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
		EFS:                 efs.New(o),
		Kinesis:             kinesis.New(o),
		SESV2:               sesv2.New(o),
		OpenSearch:          opensearch.New(o),
		Kafka:               kafka.New(o),
		VPCLattice:          vpclattice.New(o),
		WAFv2:               wafv2.New(o),
		Route53Resolver:     route53resolver.New(o),
		SFN:                 sfn.New(o),
		CloudTrail:          cloudtrail.New(o),
		Glue:                glue.New(o),
		Config:              configservice.New(o),
		GuardDuty:           guardduty.New(o),
		AccountID:           o.AccountID,
		Region:              o.Region,
		engineClosers:       o.EngineClosers(),
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
	// RunInstances materializes the instance's primary (eth0) ENI in the VPC, and
	// TerminateInstances releases it — so a running instance's interface blocks
	// DeleteSubnet / DeleteSecurityGroup the way real EC2 does.
	p.EC2.SetNetworking(p.VPC)
	// A load balancer's VpcId is derived from its subnets, matching ELBv2.
	p.ELB.SetSubnetResolver(p.VPC)
	// EFS mount targets derive their VpcId and AZ from the subnet, so all mount
	// targets of a file system share a VpcId and each reflects its subnet's zone.
	p.EFS.SetSubnetResolver(p.VPC)
	// An IamInstanceProfile passed to RunInstances resolves through IAM so the
	// role->profile->instance chain reads back on DescribeInstances.
	p.EC2.SetInstanceProfileResolver(p.IAM)
	p.SSM.SetInstanceResolver(p.EC2)
	// ECS-registered container instances surface as managed EC2 instances, so
	// #159 (ECS) composes with #300 (EC2 managed-resource visibility).
	p.ECS.SetManagedInstanceLauncher(p.EC2)
	// Engine-backed ECS tasks push their awslogs container output to CloudWatch Logs.
	p.ECS.SetLogSink(p.CloudWatchLogs)
	// A service's loadBalancers[] register/deregister RUNNING tasks with their
	// ELBv2 target group as the scheduler converges/drains the service.
	p.ECS.SetTargetRegistrar(p.ELB)
	p.Redshift.SetMonitoring(p.CloudWatch)
	// A Redshift cluster subnet group derives its VpcId and per-subnet AZs from
	// the member subnets, matching RDS/ElastiCache DB subnet groups.
	p.Redshift.SetSubnetResolver(p.VPC)
	p.EKS.SetMonitoring(p.CloudWatch)
	// An EKS cluster's resourcesVpcConfig.vpcId is derived from its subnets,
	// matching real EKS (which auto-creates the cluster SG and infers the VPC).
	p.EKS.SetSubnetResolver(p.VPC)
	p.SageMaker.SetMonitoring(p.CloudWatch)
	// CloudWatch alarm -> SNS: an alarm state transition fires its configured
	// SNS-topic actions, fanning a notification out to the topic's subscribers.
	p.CloudWatch.SetSNSPublisher(p.SNS)
	// SNS -> SQS fan-out: publishes deliver to SQS-protocol subscriptions.
	p.SNS.SetSQSDeliverer(p.SQS)
	// SNS -> Lambda fan-out: publishes invoke lambda-protocol subscriptions with
	// the SNS Records event (reuses the shared InvokeExternal choke point).
	p.SNS.SetLambdaInvoker(p.Lambda)
	// EventBridge -> targets: matched rules deliver events to their first-class
	// target types — SQS queues, Lambda functions (ASYNC), SNS topics, and Step
	// Functions state machines (ASYNC). Lambda reuses the shared InvokeExternal
	// choke point so its recursion guard bounds re-entrant event loops.
	p.EventBridge.SetSQSDeliverer(p.SQS)
	p.EventBridge.SetLambdaInvoker(p.Lambda)
	p.EventBridge.SetSNSPublisher(p.SNS)
	p.EventBridge.SetStepFunctionsStarter(p.SFN)
	// S3 event notifications deliver to their configured targets: SQS queues,
	// SNS topics, and Lambda functions.
	p.S3.SetSQSDeliverer(p.SQS)
	p.S3.SetSNSPublisher(p.SNS)
	p.S3.SetLambdaInvoker(p.Lambda)
	// DynamoDB Streams -> Lambda: writes to a stream-enabled table invoke the
	// stream's event-source-mapping targets (mirrors the S3 -> Lambda wiring).
	p.DynamoDB.SetStreamInvoker(p.Lambda)
	// SQS -> Lambda: a message sent to a queue invokes the queue's
	// event-source-mapping target(s), deleting the message on success or
	// leaving it for DLQ redrive on failure (mirrors the DynamoDB Streams wiring).
	p.SQS.SetEventSourceInvoker(p.Lambda)
	// CloudWatch Logs subscription filters -> Lambda: log events matching a
	// subscription filter's pattern are delivered (gzipped awslogs payload) to
	// the filter's Lambda destination on PutLogEvents.
	p.CloudWatchLogs.SetLambdaInvoker(p.Lambda)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAWS, o.AccountID, o.Region, awsDrivers(p),
	)

	return p
}

// awsDrivers assembles the resource-discovery driver set from the provider's
// services. It is split out of New so the factory stays within the
// function-length budget.
func awsDrivers(p *Provider) *resourcediscovery.Drivers {
	return &resourcediscovery.Drivers{
		Compute:      p.EC2,
		Networking:   p.VPC,
		Storage:      p.S3,
		Database:     p.DynamoDB,
		Serverless:   p.Lambda,
		Kubernetes:   eksDiscovery{p.EKS},
		RelationalDB: rdsDiscovery{m: p.RDS, redshift: p.Redshift},
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
		Extra: []resourcediscovery.GenericResources{
			sagemakerDiscovery{p.SageMaker},
		},
	}
}

// Close tears down any real engines wired into the provider via
// config.With<X>Engine, stopping the Docker containers or subprocesses they
// own. It is a no-op when no engine is wired — the in-memory default — and is
// safe to call more than once, since engine Close is idempotent.
func (p *Provider) Close() error {
	var errs []error

	for _, c := range p.engineClosers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// SnapshotServices returns the provider's services that support identity-
// preserving snapshotting, keyed by a stable lowercased field-name service key
// (e.g. "s3", "dynamodb", "ec2"). persist iterates this map, so the persisted
// surface automatically tracks whichever services implement
// snapshot.Snapshottable — no hand-kept registry to drift.
func (p *Provider) SnapshotServices() map[string]snapshot.Snapshottable {
	return snapshot.Discover(p)
}
