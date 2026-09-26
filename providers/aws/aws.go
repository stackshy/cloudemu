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
	"github.com/stackshy/cloudemu/v2/providers/aws/aoss"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigateway"
	"github.com/stackshy/cloudemu/v2/providers/aws/apigatewayv2"
	"github.com/stackshy/cloudemu/v2/providers/aws/appflow"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/providers/aws/appsync"
	"github.com/stackshy/cloudemu/v2/providers/aws/aps"
	"github.com/stackshy/cloudemu/v2/providers/aws/athena"
	"github.com/stackshy/cloudemu/v2/providers/aws/backup"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudformation"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudfront"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudtrail"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/providers/aws/cognito"
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
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridgescheduler"
	"github.com/stackshy/cloudemu/v2/providers/aws/fis"
	"github.com/stackshy/cloudemu/v2/providers/aws/globalaccelerator"
	"github.com/stackshy/cloudemu/v2/providers/aws/glue"
	"github.com/stackshy/cloudemu/v2/providers/aws/grafana"
	"github.com/stackshy/cloudemu/v2/providers/aws/guardduty"
	"github.com/stackshy/cloudemu/v2/providers/aws/healthlake"
	"github.com/stackshy/cloudemu/v2/providers/aws/iam"
	"github.com/stackshy/cloudemu/v2/providers/aws/kafka"
	"github.com/stackshy/cloudemu/v2/providers/aws/kendra"
	"github.com/stackshy/cloudemu/v2/providers/aws/keyspaces"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesisvideo"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/providers/aws/kmscrypto"
	"github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	"github.com/stackshy/cloudemu/v2/providers/aws/location"
	"github.com/stackshy/cloudemu/v2/providers/aws/memorydb"
	"github.com/stackshy/cloudemu/v2/providers/aws/mq"
	"github.com/stackshy/cloudemu/v2/providers/aws/mwaa"
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
	"github.com/stackshy/cloudemu/v2/providers/aws/timestreamwrite"
	"github.com/stackshy/cloudemu/v2/providers/aws/transfer"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpclattice"
	"github.com/stackshy/cloudemu/v2/providers/aws/wafv2"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// eksClusters is the slice of the EKS mock that discovery reads. It's typed as
// an interface so the vanished-cluster (NotFound) skip can be tested with a
// fake.
type eksClusters interface {
	ListClusters(ctx context.Context) ([]string, error)
	DescribeCluster(ctx context.Context, name string) (*eksdriver.Cluster, error)
	ListNodegroups(ctx context.Context, clusterName string) ([]string, error)
}

// eksDiscovery adapts the EKS mock to the resourcediscovery KubernetesClusters
// capability, so EKS clusters and their node groups surface in Resource
// Explorer. Kept in the provider package (not services/) to avoid inverting
// the layering. The discovery engine stays free of provider imports.
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
		// (NotFound) is correct to omit. Skip it rather than fail the whole
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
	Batch               *batch.Mock
	Kinesis             *kinesis.Mock
	KinesisVideo        *kinesisvideo.Mock
	Location            *location.Mock
	SESV2               *sesv2.Mock
	OpenSearch          *opensearch.Mock
	AppSync             *appsync.Mock
	AppFlow             *appflow.Mock
	MWAA                *mwaa.Mock
	MQ                  *mq.Mock
	CodeArtifact        *codeartifact.Mock
	FIS                 *fis.Mock
	Backup              *backup.Mock
	Grafana             *grafana.Mock
	Scheduler           *eventbridgescheduler.Mock
	AOSS                *aoss.Mock
	APS                 *aps.Mock
	Kendra              *kendra.Mock
	Kafka               *kafka.Mock
	VPCLattice          *vpclattice.Mock
	WAFv2               *wafv2.Mock
	Route53Resolver     *route53resolver.Mock
	SFN                 *sfn.Mock
	CloudTrail          *cloudtrail.Mock
	Glue                *glue.Mock
	Athena              *athena.Mock
	TimestreamWrite     *timestreamwrite.Mock
	HealthLake          *healthlake.Mock
	AppRunner           *apprunner.Mock
	GlobalAccelerator   *globalaccelerator.Mock
	Transfer            *transfer.Mock
	Cognito             *cognito.Mock
	Config              *configservice.Mock
	GuardDuty           *guardduty.Mock
	APIGateway          *apigateway.Mock
	APIGatewayV2        *apigatewayv2.Mock
	CloudFormation      *cloudformation.Mock
	CloudFront          *cloudfront.Mock
	ResourceDiscovery   *resourcediscovery.Engine
	AccountID           string
	Region              string
	// EnforceAuth mirrors config.Options.EnforceAuth: when true the AWS wire
	// server verifies each request's SigV4 signature. Off by default.
	EnforceAuth bool
	// Clock mirrors config.Options.Clock: it drives SigV4 timestamp-expiry and
	// STS temporary-credential expiry when EnforceAuth is on. Threaded to the
	// wire server so a FakeClock stays deterministic there too.
	Clock config.Clock

	// engineClosers holds any wired real engines that implement io.Closer, so
	// Close can cascade teardown to them. Empty for the in-memory default.
	engineClosers []io.Closer
}

// GlobalServices is the bundle of AWS services whose state is global: a single
// shared instance across every region rather than one per region. Real AWS
// isolates regional services (EC2, DynamoDB, SQS, …) per region but serves these
// from one global data plane: IAM users/roles, Route 53 hosted zones, CloudFront
// distributions, and Global Accelerator accelerators are the same in every
// region, and the S3 bucket-name namespace (S3Names) is globally unique.
//
// The multi-region region mux builds a fresh regional Provider per region but
// injects one shared GlobalServices into all of them (see NewRegional), so a
// cross-service wire from a regional service to a global one (e.g.
// EC2.SetInstanceProfileResolver(IAM)) resolves to the shared instance.
//
// S3 itself is REGIONAL (each region owns its bucket data plane so notifications
// and metrics wire to that region's SQS/SNS/Lambda/CloudWatch); only its NAME
// namespace is global, carried here as S3Names.
type GlobalServices struct {
	IAM               *iam.Mock
	Route53           *route53.Mock
	CloudFront        *cloudfront.Mock
	GlobalAccelerator *globalaccelerator.Mock
	S3Names           *s3.NameReservation
}

// newGlobalServices constructs a fresh set of shared global services. Called
// once for a standalone provider, and once per multi-region mux to seed the
// bundle every region provider then shares.
func newGlobalServices(o *config.Options) *GlobalServices {
	return &GlobalServices{
		IAM:               iam.New(o),
		Route53:           route53.New(o),
		CloudFront:        cloudfront.New(o),
		GlobalAccelerator: globalaccelerator.New(o),
		S3Names:           s3.NewNameReservation(),
	}
}

// Globals extracts the provider's shared global services so a region mux can
// inject the same bundle into freshly-built region providers via NewRegional.
func (p *Provider) Globals() *GlobalServices {
	return &GlobalServices{
		IAM:               p.IAM,
		Route53:           p.Route53,
		CloudFront:        p.CloudFront,
		GlobalAccelerator: p.GlobalAccelerator,
		S3Names:           p.S3.NameReservation(),
	}
}

// New creates a new AWS provider with all mock services, owning its own set of
// global services (the single-region default).
func New(opts ...config.Option) *Provider {
	return newProvider(config.NewOptions(opts...), nil)
}

// NewRegional creates a regional AWS provider that SHARES the given global
// services instead of creating its own, so IAM/Route53/CloudFront/Global
// Accelerator state and the S3 bucket-name namespace are common to every region
// built with the same bundle. Pass config.WithRegion(region) to stamp the region
// onto this provider's regional resources. When shared is nil it behaves exactly
// like New.
func NewRegional(shared *GlobalServices, opts ...config.Option) *Provider {
	return newProvider(config.NewOptions(opts...), shared)
}

// newProviderMocks constructs every service mock for one provider, injecting the
// shared global services (g) so a region provider shares IAM/Route53/CloudFront/
// GlobalAccelerator (and the S3 name namespace) with its siblings while owning
// fresh regional services.
func newProviderMocks(o *config.Options, g *GlobalServices) *Provider {
	return &Provider{
		S3:                  s3.New(o),
		EC2:                 ec2.New(o),
		DynamoDB:            dynamodb.New(o),
		Lambda:              lambda.New(o),
		VPC:                 vpc.New(o),
		CloudWatch:          cloudwatch.New(o),
		IAM:                 g.IAM,
		Route53:             g.Route53,
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
		Batch:               batch.New(o),
		Kinesis:             kinesis.New(o),
		KinesisVideo:        kinesisvideo.New(o),
		Location:            location.New(o),
		SESV2:               sesv2.New(o),
		OpenSearch:          opensearch.New(o),
		AppSync:             appsync.New(o),
		AppFlow:             appflow.New(o),
		MWAA:                mwaa.New(o),
		MQ:                  mq.New(o),
		CodeArtifact:        codeartifact.New(o),
		FIS:                 fis.New(o),
		Backup:              backup.New(o),
		Grafana:             grafana.New(o),
		Scheduler:           eventbridgescheduler.New(o),
		AOSS:                aoss.New(o),
		APS:                 aps.New(o),
		Kendra:              kendra.New(o),
		Kafka:               kafka.New(o),
		VPCLattice:          vpclattice.New(o),
		WAFv2:               wafv2.New(o),
		Route53Resolver:     route53resolver.New(o),
		SFN:                 sfn.New(o),
		CloudTrail:          cloudtrail.New(o),
		Glue:                glue.New(o),
		Athena:              athena.New(o),
		TimestreamWrite:     timestreamwrite.New(o),
		HealthLake:          healthlake.New(o),
		AppRunner:           apprunner.New(o),
		GlobalAccelerator:   g.GlobalAccelerator,
		Transfer:            transfer.New(o),
		Cognito:             cognito.New(o),
		Config:              configservice.New(o),
		GuardDuty:           guardduty.New(o),
		APIGateway:          apigateway.New(o),
		APIGatewayV2:        apigatewayv2.New(o),
		CloudFront:          g.CloudFront,
		AccountID:           o.AccountID,
		Region:              o.Region,
		EnforceAuth:         o.EnforceAuth,
		Clock:               o.Clock,
		engineClosers:       o.EngineClosers(),
	}
}

// newProvider builds a provider, using the supplied shared global services when
// non-nil (multi-region) or a fresh bundle when nil (standalone). The global
// struct fields are assigned BEFORE the cross-service Set* wiring runs, so every
// regional→global wire (e.g. EC2→IAM) points at the shared instance.
func newProvider(o *config.Options, shared *GlobalServices) *Provider {
	g := shared
	if g == nil {
		g = newGlobalServices(o)
	}

	p := newProviderMocks(o, g)
	// S3 is regional but shares the global bucket-name namespace, so a name taken
	// in any region blocks a create in another (BucketAlreadyExists).
	p.S3.SetNameReservation(g.S3Names)
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
	p.Kinesis.SetMonitoring(p.CloudWatch)
	p.SFN.SetMonitoring(p.CloudWatch)
	p.APIGateway.SetMonitoring(p.CloudWatch)
	p.Athena.SetMonitoring(p.CloudWatch)
	p.RDS.SetSubnetResolver(p.VPC)
	p.ElastiCache.SetSubnetResolver(p.VPC)
	p.EC2.SetSubnetResolver(p.VPC)
	// RunInstances materializes the instance's primary (eth0) ENI in the VPC, and
	// TerminateInstances releases it. That's why a running instance's interface
	// blocks DeleteSubnet / DeleteSecurityGroup the way real EC2 does.
	p.EC2.SetNetworking(p.VPC)
	// A load balancer's VpcId is derived from its subnets, matching ELBv2.
	p.ELB.SetSubnetResolver(p.VPC)
	// EFS mount targets derive their VpcId and AZ from the subnet, so all mount
	// targets of a file system share a VpcId and each reflects its subnet's zone.
	p.EFS.SetSubnetResolver(p.VPC)
	// An IamInstanceProfile passed to RunInstances resolves through IAM so the
	// role->profile->instance chain reads back on DescribeInstances.
	p.EC2.SetInstanceProfileResolver(p.IAM)
	// Secrets Manager and SSM SecureString values are encrypted through real KMS
	// (envelope encryption): a KmsKeyId is validated to exist, the value is stored
	// as genuine ciphertext, and a later disabled/deleted key makes reads fail as
	// in AWS. Both services share one Envelope over the KMS backend.
	kmsCrypto := kmscrypto.New(p.KMS)
	p.SecretsManager.SetKMSCrypto(kmsCrypto)
	p.SSM.SetKMSCrypto(kmsCrypto)
	p.SSM.SetInstanceResolver(p.EC2)
	// ECS-registered container instances surface as managed EC2 instances, so
	// #159 (ECS) composes with #300 (EC2 managed-resource visibility).
	p.ECS.SetManagedInstanceLauncher(p.EC2)
	// Engine-backed ECS tasks push their awslogs container output to CloudWatch Logs.
	p.ECS.SetLogSink(p.CloudWatchLogs)
	// Lambda invocations write START/END/REPORT lines (and captured stdout/stderr
	// on the real-engine path) to CloudWatch Logs under /aws/lambda/<name>.
	p.Lambda.SetLogSink(p.CloudWatchLogs)
	// A service's loadBalancers[] register/deregister RUNNING tasks with their
	// ELBv2 target group as the scheduler converges/drains the service.
	p.ECS.SetTargetRegistrar(p.ELB)
	p.Redshift.SetMonitoring(p.CloudWatch)
	// A Redshift cluster subnet group derives its VpcId and per-subnet AZs from
	// the member subnets, matching RDS/ElastiCache DB subnet groups.
	p.Redshift.SetSubnetResolver(p.VPC)
	// A Redshift event subscription checks that its SNS topic exists.
	p.Redshift.SetTopicLookup(p.SNS)
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
	// Lambda async failure -> DLQ / destination: a failed asynchronous (Event)
	// invoke routes its event to the function's DeadLetterConfig queue/topic and
	// its OnFailure/OnSuccess async destinations via the SQS/SNS mocks. Re-entry
	// is bounded by the shared InvokeExternal recursion guard.
	p.Lambda.SetAsyncDestinationTargets(p.SQS, p.SNS)
	// EventBridge -> targets: matched rules deliver events to their first-class
	// target types: SQS queues, Lambda functions (ASYNC), SNS topics, and Step
	// Functions state machines (ASYNC). Lambda reuses the shared InvokeExternal
	// choke point so its recursion guard bounds re-entrant event loops.
	p.EventBridge.SetSQSDeliverer(p.SQS)
	p.EventBridge.SetLambdaInvoker(p.Lambda)
	p.EventBridge.SetSNSPublisher(p.SNS)
	p.EventBridge.SetStepFunctionsStarter(p.SFN)
	wireLifecycleEvents(p)
	// Step Functions -> Lambda: a Task state (arn:aws:states:::lambda:invoke or a
	// bare Lambda function ARN) invokes the function synchronously through the
	// recursion-guarded InvokeSync seam, so a Task->Lambda->StartExecution->Task
	// cycle terminates at recursionguard.MaxDepth instead of overflowing.
	p.SFN.SetLambdaSyncInvoker(p.Lambda)
	// API Gateway -> Lambda: a data-plane request to a deployed REST API resolves
	// its AWS_PROXY integration to a Lambda ARN and invokes the function through
	// the same recursion-guarded InvokeSync seam, returning the function's
	// {statusCode,headers,body} as the HTTP response.
	p.APIGateway.SetLambdaInvoker(p.Lambda)
	wirePostBuildServices(o, p)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAWS, o.AccountID, o.Region, awsDrivers(p),
	)

	return p
}

// wirePostBuildServices runs the cross-service wiring that must happen after
// every service mock exists: the Lambda event-source delivery seams, and the
// CloudFormation orchestrator, whose provisioner registry reads the live service
// mocks. It is split out of New so the factory stays within the function-length
// budget; the wiring-parity test scans this helper as if it were New().
func wirePostBuildServices(o *config.Options, p *Provider) {
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
	// Kinesis -> Lambda: records written to a stream (PutRecord/PutRecords)
	// deliver a Kinesis-shaped event batch to the stream's event-source-mapping
	// target(s) (mirrors the DynamoDB Streams -> Lambda wiring).
	p.Kinesis.SetLambdaInvoker(p.Lambda)
	// CloudWatch Logs subscription filters -> Lambda: log events matching a
	// subscription filter's pattern are delivered (gzipped awslogs payload) to
	// the filter's Lambda destination on PutLogEvents.
	p.CloudWatchLogs.SetLambdaInvoker(p.Lambda)
	// CloudFormation is an orchestrator: it provisions each stack resource by
	// calling the matching service driver, so its registry is built from the
	// live mocks rather than a store of its own.
	p.CloudFormation = cloudformation.New(o)
	p.CloudFormation.SetRegistry(cloudformationRegistry(p))
	p.CloudFormation.SetTemplateFetcher(cloudformationTemplateFetcher(p))
}

// wireLifecycleEvents points each service's native lifecycle events at the
// default EventBridge bus, the way real AWS services publish them to the
// account's default bus automatically (EC2 instance state changes, ECS task
// state changes, Step Functions execution status changes, ...). A rule on the
// default bus matching the real event pattern then fires in cloudemu too.
//
// EKS is deliberately absent: real EKS publishes no native cluster/nodegroup
// status events to EventBridge (only CloudTrail API-call events).
func wireLifecycleEvents(p *Provider) {
	p.EC2.SetEventPublisher(p.EventBridge)
	p.ECS.SetEventPublisher(p.EventBridge)
	p.SFN.SetEventPublisher(p.EventBridge)
	p.ECR.SetEventPublisher(p.EventBridge)
	p.SSM.SetEventPublisher(p.EventBridge)
	p.Glue.SetEventPublisher(p.EventBridge)
	p.CloudWatch.SetEventPublisher(p.EventBridge)
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
		// Taggers extends the Resource Groups Tagging API to every other AWS
		// service that carries a tag store but has no discovery-driver hook here,
		// routing each ARN to the service's own tag methods.
		Taggers: awsTaggers(p),
		Extra: []resourcediscovery.GenericResources{
			sagemakerDiscovery{p.SageMaker},
			taggedDiscovery{p},
		},
	}
}

// Close tears down any real engines wired into the provider via
// config.With<X>Engine, stopping the Docker containers or subprocesses they
// own. It is a no-op when no engine is wired (the in-memory default) and is
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
// snapshot.Snapshottable. No hand-kept registry to drift.
func (p *Provider) SnapshotServices() map[string]snapshot.Snapshottable {
	return snapshot.Discover(p)
}

// globalSnapshotKeys are the lowercased field names of the services whose state
// is global (shared across regions). persist captures these once under the "aws"
// key; everything else is captured per region under "aws@<region>". S3 is NOT
// here. It is regional (only its name namespace is global, and that is rebuilt
// from restored buckets, not snapshotted).
//
//nolint:gochecknoglobals // an immutable classification set, the multi-region counterpart of the field map.
var globalSnapshotKeys = map[string]struct{}{
	"iam":               {},
	"route53":           {},
	"cloudfront":        {},
	"globalaccelerator": {},
}

// GlobalSnapshotServices returns only the shared global services' snapshotters,
// keyed by service name. The region mux snapshots these once (under "aws")
// rather than once per region, since every region provider shares the same
// instances.
func (p *Provider) GlobalSnapshotServices() map[string]snapshot.Snapshottable {
	all := snapshot.Discover(p)
	out := make(map[string]snapshot.Snapshottable, len(globalSnapshotKeys))

	for k := range globalSnapshotKeys {
		if s, ok := all[k]; ok {
			out[k] = s
		}
	}

	return out
}

// RegionalSnapshotServices returns every snapshotter EXCEPT the shared global
// ones, so the region mux captures a region's own regional state under
// "aws@<region>" without duplicating the global services in each region.
func (p *Provider) RegionalSnapshotServices() map[string]snapshot.Snapshottable {
	all := snapshot.Discover(p)
	for k := range globalSnapshotKeys {
		delete(all, k)
	}

	return all
}
