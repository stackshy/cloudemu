// Package aws assembles CloudEmu's AWS-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package aws

import (
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/server"
	acmsrv "github.com/stackshy/cloudemu/v2/server/aws/acm"
	"github.com/stackshy/cloudemu/v2/server/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/server/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/server/aws/bedrockagentruntime"
	cloudtrailsrv "github.com/stackshy/cloudemu/v2/server/aws/cloudtrail"
	"github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
	cloudwatchlogssrv "github.com/stackshy/cloudemu/v2/server/aws/cloudwatchlogs"
	configservicesrv "github.com/stackshy/cloudemu/v2/server/aws/configservice"
	"github.com/stackshy/cloudemu/v2/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/v2/server/aws/ec2"
	"github.com/stackshy/cloudemu/v2/server/aws/ecr"
	ecssrv "github.com/stackshy/cloudemu/v2/server/aws/ecs"
	efssrv "github.com/stackshy/cloudemu/v2/server/aws/efs"
	"github.com/stackshy/cloudemu/v2/server/aws/eks"
	"github.com/stackshy/cloudemu/v2/server/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/server/aws/elbv2"
	"github.com/stackshy/cloudemu/v2/server/aws/eventbridge"
	gluesrv "github.com/stackshy/cloudemu/v2/server/aws/glue"
	guarddutysrv "github.com/stackshy/cloudemu/v2/server/aws/guardduty"
	"github.com/stackshy/cloudemu/v2/server/aws/iam"
	keyspacessrv "github.com/stackshy/cloudemu/v2/server/aws/keyspaces"
	kinesissrv "github.com/stackshy/cloudemu/v2/server/aws/kinesis"
	kmssrv "github.com/stackshy/cloudemu/v2/server/aws/kms"
	"github.com/stackshy/cloudemu/v2/server/aws/lambda"
	memorydbsrv "github.com/stackshy/cloudemu/v2/server/aws/memorydb"
	networkfirewallsrv "github.com/stackshy/cloudemu/v2/server/aws/networkfirewall"
	opensearchsrv "github.com/stackshy/cloudemu/v2/server/aws/opensearch"
	"github.com/stackshy/cloudemu/v2/server/aws/rds"
	"github.com/stackshy/cloudemu/v2/server/aws/redshift"
	"github.com/stackshy/cloudemu/v2/server/aws/resourceexplorer2"
	"github.com/stackshy/cloudemu/v2/server/aws/resourcegroupstaggingapi"
	"github.com/stackshy/cloudemu/v2/server/aws/route53"
	route53resolversrv "github.com/stackshy/cloudemu/v2/server/aws/route53resolver"
	"github.com/stackshy/cloudemu/v2/server/aws/s3"
	sagemakersrv "github.com/stackshy/cloudemu/v2/server/aws/sagemaker"
	secretsmanagersrv "github.com/stackshy/cloudemu/v2/server/aws/secretsmanager"
	sesv2srv "github.com/stackshy/cloudemu/v2/server/aws/sesv2"
	sfnsrv "github.com/stackshy/cloudemu/v2/server/aws/sfn"
	"github.com/stackshy/cloudemu/v2/server/aws/sns"
	"github.com/stackshy/cloudemu/v2/server/aws/sqs"
	ssmsrv "github.com/stackshy/cloudemu/v2/server/aws/ssm"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
	vpclatticesrv "github.com/stackshy/cloudemu/v2/server/aws/vpclattice"
	wafv2srv "github.com/stackshy/cloudemu/v2/server/aws/wafv2"
	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
	bedrockagentdriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	bedrockagentruntimedriver "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	cloudtraildriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	configservicedriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	efsdriver "github.com/stackshy/cloudemu/v2/services/efs/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
	guarddutydriver "github.com/stackshy/cloudemu/v2/services/guardduty/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	opensearchdriver "github.com/stackshy/cloudemu/v2/services/opensearch/driver"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	route53resolverdriver "github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	sesv2driver "github.com/stackshy/cloudemu/v2/services/sesv2/driver"
	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	vpclatticedriver "github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
	wafv2driver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// Drivers bundles the driver interfaces the AWS server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
type Drivers struct {
	S3                  storagedriver.Bucket
	DynamoDB            dbdriver.Database
	EC2                 computedriver.Compute
	VPC                 netdriver.Networking
	CloudWatch          mondriver.Monitoring
	Lambda              sdrv.Serverless
	SQS                 mqdriver.MessageQueue
	RDS                 rdbdriver.RelationalDB
	Redshift            rdbdriver.RelationalDB
	EKS                 eksdriver.EKS
	IAM                 iamdriver.IAM
	ECR                 crdriver.ContainerRegistry
	Bedrock             bedrockdriver.Bedrock
	BedrockAgent        bedrockagentdriver.BedrockAgent
	BedrockAgentRuntime bedrockagentruntimedriver.BedrockAgentRuntime
	SageMaker           sagemakerdriver.Service
	// ECS serves the Amazon ECS JSON 1.1 protocol (X-Amz-Target prefix
	// AmazonEC2ContainerServiceV20141113.) against the ecs driver.
	ECS ecsdriver.ECS

	// VPCLattice serves the AWS VPC Lattice REST-JSON API (path + method
	// routing) against the vpclattice driver.
	VPCLattice vpclatticedriver.VPCLattice

	// WAFv2 serves the AWS WAFv2 JSON 1.1 protocol (X-Amz-Target prefix
	// "AWSWAF_20190729.") against the wafv2 driver.
	WAFv2 wafv2driver.WAFV2

	// EFS serves the AWS EFS REST-JSON API (path + method routing under the
	// /2015-02-01/ version prefix) against the efs driver.
	EFS efsdriver.EFS

	// SESV2 serves the AWS SES v2 REST-JSON API (path + method routing under the
	// /v2/email/ version prefix) against the sesv2 driver.
	SESV2 sesv2driver.SESV2

	// OpenSearch serves the Amazon OpenSearch Service REST-JSON API (path +
	// method routing under the /2021-01-01/ version prefix) against the
	// opensearch driver.
	OpenSearch opensearchdriver.OpenSearch

	// Route53Resolver serves the AWS Route 53 Resolver JSON 1.1 protocol
	// (X-Amz-Target prefix "Route53Resolver.") against the route53resolver driver.
	Route53Resolver route53resolverdriver.Route53Resolver
	// SecretsManager serves the Secrets Manager JSON 1.1 protocol against
	// the secrets driver.
	SecretsManager secretsdriver.Secrets
	// KMS serves the KMS JSON 1.1 protocol against the kms driver.
	KMS kmsdriver.KMS
	// ACM serves the Certificate Manager JSON 1.1 protocol against the acm driver.
	ACM acmdriver.ACM

	// Kinesis serves the Kinesis Data Streams JSON 1.1 protocol against the kinesis driver.
	Kinesis kinesisdriver.Kinesis
	// CloudTrail serves the CloudTrail JSON 1.1 protocol (X-Amz-Target prefix
	// "CloudTrail_20131101.") against the cloudtrail driver.
	CloudTrail cloudtraildriver.CloudTrail
	// Glue serves the AWS Glue JSON 1.1 protocol (X-Amz-Target prefix
	// "AWSGlue.") against the glue driver.
	Glue gluedriver.Glue
	// Config serves the AWS Config JSON 1.1 protocol (X-Amz-Target prefix
	// "StarlingDoveService.") against the configservice driver.
	Config configservicedriver.Config
	// GuardDuty serves the Amazon GuardDuty REST-JSON API (path + method routing,
	// no version prefix) against the guardduty driver. It must register before
	// the S3 catch-all (see the GuardDuty handler's Matches doc).
	GuardDuty guarddutydriver.GuardDuty
	// SSM serves the Systems Manager Parameter Store JSON 1.1 protocol against
	// the parameterstore driver.
	SSM ssmdriver.ParameterStore
	// CloudWatchLogs serves the CloudWatch Logs JSON 1.1 protocol against the
	// logging driver.
	CloudWatchLogs logdriver.Logging
	// Route53 serves the Route 53 REST/XML protocol against the dns driver.
	Route53 dnsdriver.DNS
	// ELB serves the Elastic Load Balancing v2 (ALB/NLB) query protocol
	// against the loadbalancer driver.
	ELB lbdriver.LoadBalancer
	// EventBridge serves the EventBridge JSON 1.1 protocol against the eventbus
	// driver.
	EventBridge ebdriver.EventBus
	// ElastiCache serves the ElastiCache query protocol (cluster control plane)
	// against the cache driver.
	ElastiCache cachedriver.Cache
	// Keyspaces serves the Amazon Keyspaces JSON 1.0 protocol (Cassandra
	// control plane) against the keyspaces driver.
	Keyspaces ksdriver.Keyspaces
	// MemoryDB serves the AWS MemoryDB JSON 1.1 protocol (Redis/Valkey cluster
	// control plane) against the memorydb driver.
	MemoryDB mdbdriver.MemoryDB
	// NetworkFirewall serves the AWS Network Firewall JSON 1.0 protocol against
	// the networkfirewall driver.
	NetworkFirewall nfdriver.NetworkFirewall
	// SNS serves the SNS query protocol against the notification driver.
	SNS notifdriver.Notification
	// SFN serves the Step Functions JSON 1.0 protocol (X-Amz-Target prefix
	// "AWSStepFunctions.") against the sfn driver.
	SFN sfndriver.SFN
	// STS serves the AWS STS query protocol (GetCallerIdentity, AssumeRole,
	// GetSessionToken). It has no backing driver — identity is derived from
	// AccountID and Region — so it is gated on this bool. Enable it so SDK code
	// paths that call sts:GetCallerIdentity or sts:AssumeRole on init succeed.
	STS bool
	// K8sAPI is the shared in-memory Kubernetes data-plane API server. It is
	// shared with azureserver.Drivers.K8sAPI and gcpserver.Drivers.K8sAPI so a
	// kubeconfig issued by any provider's control plane (EKS/AKS/GKE) reaches
	// the same backend. Leave nil to disable Kubernetes data-plane support.
	K8sAPI *kubernetes.APIServer
	// ResourceDiscovery is the cross-service inventory engine. Required to
	// serve Resource Explorer 2 and Resource Groups Tagging API requests.
	// Leave nil to omit both handlers. AccountID and Region are needed for
	// Resource Explorer to construct view/index ARNs.
	ResourceDiscovery *resourcediscovery.Engine
	AccountID         string
	Region            string
}

// DriversFrom builds a Drivers bundle wiring every service handler to the
// matching mock on p, so a standalone binary can serve a fully-constructed
// provider without hand-mapping each field. STS is enabled (identity is
// derived from AccountID/Region and is safe in standalone). K8sAPI is left
// nil for the caller to inject when a shared cluster is desired.
func DriversFrom(p *awsprovider.Provider) Drivers {
	return Drivers{
		S3:                  p.S3,
		DynamoDB:            p.DynamoDB,
		EC2:                 p.EC2,
		VPC:                 p.VPC,
		CloudWatch:          p.CloudWatch,
		Lambda:              p.Lambda,
		SQS:                 p.SQS,
		RDS:                 p.RDS,
		Redshift:            p.Redshift,
		EKS:                 p.EKS,
		IAM:                 p.IAM,
		ECR:                 p.ECR,
		Bedrock:             p.Bedrock,
		BedrockAgent:        p.BedrockAgent,
		BedrockAgentRuntime: p.BedrockAgentRuntime,
		SageMaker:           p.SageMaker,
		ECS:                 p.ECS,
		VPCLattice:          p.VPCLattice,
		WAFv2:               p.WAFv2,
		EFS:                 p.EFS,
		SESV2:               p.SESV2,
		OpenSearch:          p.OpenSearch,
		Route53Resolver:     p.Route53Resolver,
		SecretsManager:      p.SecretsManager,
		KMS:                 p.KMS,
		ACM:                 p.ACM,
		Kinesis:             p.Kinesis,
		CloudTrail:          p.CloudTrail,
		Glue:                p.Glue,
		Config:              p.Config,
		GuardDuty:           p.GuardDuty,
		SSM:                 p.SSM,
		CloudWatchLogs:      p.CloudWatchLogs,
		Route53:             p.Route53,
		ELB:                 p.ELB,
		EventBridge:         p.EventBridge,
		ElastiCache:         p.ElastiCache,
		Keyspaces:           p.Keyspaces,
		MemoryDB:            p.MemoryDB,
		NetworkFirewall:     p.NetworkFirewall,
		SNS:                 p.SNS,
		SFN:                 p.SFN,
		STS:                 true,
		K8sAPI:              nil, // injected by the caller when a shared cluster is desired
		ResourceDiscovery:   p.ResourceDiscovery,
		AccountID:           p.AccountID,
		Region:              p.Region,
	}
}

// NewFromProvider returns a server serving every service on p, using the
// wiring built by DriversFrom.
func NewFromProvider(p *awsprovider.Provider) *server.Server {
	return New(DriversFrom(p))
}

// New returns a server that speaks the AWS SDK wire protocols for every
// non-nil driver in d. Handlers are registered most-specific-first so the
// dispatch is unambiguous:
//
//   - CloudWatch matches on Smithy-Protocol: rpc-v2-cbor header.
//   - DynamoDB matches on X-Amz-Target header (JSON-RPC).
//   - RDS matches form-encoded POSTs whose Action is one of the known RDS
//     operations. It must register before EC2 because both speak the AWS
//     query protocol on the same content type.
//   - EC2 matches on Action= (form-encoded POST or query string). The EC2
//     handler also serves VPC and Auto Scaling ops since real AWS uses the
//     same query-protocol endpoint for all of them.
//   - Lambda matches on the /2015-03-31/functions path prefix and must
//     register before S3 so its REST URLs aren't swallowed by the catch-all.
//   - K8sAPI matches /k8s/{uid}/... — disjoint from every other AWS path;
//     registered before S3's REST fallback.
//   - S3 is the REST fallback.
//
// keeps the caller API ergonomic (awsserver.New(Drivers{...})).
//
//nolint:gocritic,gocyclo,funlen,gocognit // by-value Drivers for ergonomics; one if-per-driver dispatch grows with the bundle.
func New(d Drivers) *server.Server {
	srv := server.New()

	if d.CloudWatch != nil {
		// The VPC driver optionally supplies derived AWS/IPAM metrics; surface
		// them through CloudWatch when it implements the capability.
		ipamMetrics, _ := d.VPC.(netdriver.IPAMMetrics)
		cw := cloudwatch.New(d.CloudWatch)
		cw.SetIPAMMetrics(ipamMetrics)
		srv.Register(cw)
	}

	if d.DynamoDB != nil {
		srv.Register(dynamodb.New(d.DynamoDB))
	}

	// SQS shares the X-Amz-Target header with DynamoDB but uses a different
	// prefix (AmazonSQS.* vs DynamoDB_20120810.*); their Matches predicates
	// are mutually exclusive.
	if d.SQS != nil {
		srv.Register(sqs.New(d.SQS))
	}

	// Resource Groups Tagging API: X-Amz-Target prefix
	// ResourceGroupsTaggingAPI_20170126.* — disjoint from DynamoDB/SQS.
	if d.ResourceDiscovery != nil {
		srv.Register(resourcegroupstaggingapi.New(d.ResourceDiscovery))
	}

	// RDS must be registered before EC2: both speak AWS query-protocol on
	// POST + form-encoded bodies, and Server matches in registration order.
	// RDS's Matches is action-specific, so a request bound for EC2 will fall
	// through to the EC2 handler unchanged.
	if d.RDS != nil {
		srv.Register(rds.New(d.RDS))
	}

	// IAM also speaks AWS query-protocol; its action set is disjoint from
	// RDS, Redshift, and EC2. Registered before EC2 for the same reason.
	if d.IAM != nil {
		srv.Register(iam.New(d.IAM))
	}

	if d.ECR != nil {
		srv.Register(ecr.New(d.ECR))
	}

	// Secrets Manager matches the X-Amz-Target prefix "secretsmanager." —
	// disjoint from DynamoDB, SQS, ECR, SageMaker, and the tagging API.
	if d.SecretsManager != nil {
		srv.Register(secretsmanagersrv.New(d.SecretsManager))
	}

	// KMS matches the X-Amz-Target prefix "TrentService." — disjoint from
	// DynamoDB, SQS, ECR, SageMaker, Secrets Manager, and the tagging API.
	if d.KMS != nil {
		srv.Register(kmssrv.New(d.KMS))
	}

	// ACM matches the X-Amz-Target prefix "CertificateManager." — disjoint
	// from the other JSON 1.1 services.
	if d.ACM != nil {
		srv.Register(acmsrv.New(d.ACM))
	}

	// Step Functions matches the X-Amz-Target prefix "AWSStepFunctions." —
	// disjoint from every other JSON-RPC service, so registration order is free.
	if d.SFN != nil {
		srv.Register(sfnsrv.New(d.SFN))
	}

	// Kinesis matches the X-Amz-Target prefix "Kinesis_20131202." — disjoint
	// from the other JSON 1.1 services, so registration order is unconstrained.
	if d.Kinesis != nil {
		srv.Register(kinesissrv.New(d.Kinesis))
	}

	// CloudTrail matches the X-Amz-Target prefix "CloudTrail_20131101." —
	// disjoint from the other JSON 1.1 services, so registration order is free.
	if d.CloudTrail != nil {
		srv.Register(cloudtrailsrv.New(d.CloudTrail))
	}

	// Glue matches the X-Amz-Target prefix "AWSGlue." — disjoint from the other
	// JSON 1.1 services, so registration order is unconstrained.
	if d.Glue != nil {
		srv.Register(gluesrv.New(d.Glue))
	}

	// AWS Config matches the X-Amz-Target prefix "StarlingDoveService." —
	// disjoint from the other JSON 1.1 services, so registration order is free.
	if d.Config != nil {
		srv.Register(configservicesrv.New(d.Config, d.AccountID, d.Region))
	}

	// WAFv2 matches the X-Amz-Target prefix "AWSWAF_20190729." — disjoint from
	// the other JSON 1.1 services, so registration order is unconstrained.
	if d.WAFv2 != nil {
		srv.Register(wafv2srv.New(d.WAFv2))
	}

	// ECS matches the X-Amz-Target prefix "AmazonEC2ContainerServiceV20141113."
	// — disjoint from DynamoDB, SQS, ECR, SageMaker, Secrets Manager, SSM,
	// EventBridge, and the tagging API, so registration order is unconstrained.
	if d.ECS != nil {
		srv.Register(ecssrv.New(d.ECS))
	}

	// VPC Lattice is a REST/JSON service rooted at path prefixes like
	// /servicenetworks, /services, /targetgroups; its Matches predicate gates on
	// method+shape and must run before the S3 catch-all.
	if d.VPCLattice != nil {
		srv.Register(vpclatticesrv.New(d.VPCLattice))
	}

	// EFS uses REST-JSON path routing under the /2015-02-01/ version prefix; its
	// Matches predicate gates on that prefix, so it must run before the S3
	// catch-all (no real bucket path begins with /2015-02-01/).
	if d.EFS != nil {
		srv.Register(efssrv.New(d.EFS))
	}

	// SES v2 uses REST-JSON path routing under the /v2/email/ version prefix; its
	// Matches predicate gates on that prefix, so it must run before the S3
	// catch-all (no real bucket path begins with /v2/email/).
	if d.SESV2 != nil {
		srv.Register(sesv2srv.New(d.SESV2))
	}

	// OpenSearch uses REST-JSON path routing under the /2021-01-01/ version
	// prefix; its Matches predicate gates on that prefix, so it must run before
	// the S3 catch-all (no real bucket path begins with /2021-01-01/).
	if d.OpenSearch != nil {
		srv.Register(opensearchsrv.New(d.OpenSearch))
	}

	// Route53Resolver matches the X-Amz-Target prefix "Route53Resolver." —
	// disjoint from the other JSON 1.1 services, so registration order is free.
	if d.Route53Resolver != nil {
		srv.Register(route53resolversrv.New(d.Route53Resolver))
	}

	// SSM Parameter Store matches the X-Amz-Target prefix "AmazonSSM." —
	// disjoint from DynamoDB, SQS, ECR, SageMaker, Secrets Manager, EventBridge,
	// CloudWatch Logs, and the tagging API.
	if d.SSM != nil {
		srv.Register(ssmsrv.New(d.SSM))
	}

	// EventBridge matches the X-Amz-Target prefix "AWSEvents." — disjoint from
	// DynamoDB, SQS, ECR, SageMaker, Secrets Manager, and the tagging API.
	if d.EventBridge != nil {
		srv.Register(eventbridge.New(d.EventBridge, d.AccountID, d.Region))
	}

	// CloudWatch Logs matches the X-Amz-Target prefix "Logs_20140328." —
	// disjoint from DynamoDB, SQS, Secrets Manager, ECR, SageMaker, and the
	// tagging API, so registration order relative to them is unconstrained.
	if d.CloudWatchLogs != nil {
		srv.Register(cloudwatchlogssrv.New(d.CloudWatchLogs))
	}

	// Redshift sits with the other query-protocol handlers before the EC2
	// catch-all. Its action set (CreateCluster, DescribeClusters, …) is
	// disjoint from RDS's (CreateDBInstance, …), from IAM's (CreateUser, …),
	// and from EC2's (RunInstances, …), so no shadowing occurs.
	if d.Redshift != nil {
		srv.Register(redshift.New(d.Redshift))
	}

	// ELBv2 also speaks AWS query-protocol; its action set (CreateLoadBalancer,
	// CreateTargetGroup, CreateListener, RegisterTargets, …) is disjoint from
	// RDS, IAM, Redshift, and EC2. It must register before the EC2 catch-all so
	// the EC2 handler doesn't claim ELBv2 form bodies first.
	if d.ELB != nil {
		srv.Register(elbv2.New(d.ELB))
	}

	// ElastiCache is another AWS query-protocol handler; register before the
	// EC2 catch-all. Its action set (CreateCacheCluster, DescribeCacheClusters,
	// DeleteCacheCluster) is disjoint from RDS, Redshift, IAM, and EC2, so no
	// shadowing occurs.
	if d.ElastiCache != nil {
		srv.Register(elasticache.New(d.ElastiCache))
	}

	// MemoryDB speaks AWS JSON 1.1 and matches on the "AmazonMemoryDB." target
	// prefix, so its dispatch is disjoint from every query-protocol handler and
	// registration order relative to the EC2 catch-all does not matter.
	if d.MemoryDB != nil {
		srv.Register(memorydbsrv.New(d.MemoryDB))
	}

	// Network Firewall speaks AWS JSON 1.0 and matches on the
	// "NetworkFirewall_20201112." target prefix, so its dispatch is disjoint
	// from every other handler.
	if d.NetworkFirewall != nil {
		srv.Register(networkfirewallsrv.New(d.NetworkFirewall))
	}

	// Keyspaces speaks AWS JSON 1.0 and matches on the "KeyspacesService." target
	// prefix, so its dispatch is disjoint from every other handler.
	if d.Keyspaces != nil {
		srv.Register(keyspacessrv.New(d.Keyspaces))
	}

	// SNS also speaks the AWS query protocol; its action set (CreateTopic,
	// Subscribe, Publish, …) is disjoint from RDS, Redshift, IAM, and EC2, so
	// no shadowing occurs. Registered before the EC2 catch-all.
	if d.SNS != nil {
		srv.Register(sns.New(d.SNS))
	}

	// STS also speaks the AWS query protocol; its action set (GetCallerIdentity,
	// AssumeRole, GetSessionToken) is disjoint from RDS, Redshift, IAM, ELBv2,
	// ElastiCache, SNS, and EC2, so no shadowing occurs. It has no driver, so
	// it's gated on the STS bool. Registered before the EC2 catch-all.
	if d.STS {
		srv.Register(stssrv.New(d.AccountID, d.Region))
	}

	if d.EC2 != nil || d.VPC != nil {
		srv.Register(ec2.New(d.EC2, d.VPC))
	}

	if d.Lambda != nil {
		srv.Register(lambda.New(d.Lambda))
	}

	// EKS is a REST/JSON service rooted at /clusters. It must register
	// GuardDuty uses REST-JSON path + method routing with NO version prefix, so
	// its Matches predicate gates on the first path segment being a known
	// GuardDuty root (detector, admin, invitation, tags, malware-scan,
	// malware-scans, malware-protection-plan, object-malware-scan, organization).
	// It MUST register before S3's permissive REST catch-all. It also registers
	// before EKS because both use the shared /tags/{ResourceArn} REST path and
	// EKS's Matches claims every /tags request; the GuardDuty handler only claims
	// that path for GuardDuty ARNs, so EKS (and other services') tag requests
	// fall through to their own handlers.
	if d.GuardDuty != nil {
		srv.Register(guarddutysrv.New(d.GuardDuty))
	}

	// before S3 because S3 is the permissive REST fallback that would
	// otherwise claim the same path. EKS's Matches predicate is rooted
	// at /clusters specifically so it doesn't shadow other REST URLs.
	if d.EKS != nil {
		srv.Register(eks.New(d.EKS))
	}

	// Bedrock is a REST/JSON service rooted at /foundation-models,
	// /model-customization-jobs, /custom-models, and /model/{id}/{invoke,
	// converse}. It must register before S3 because S3 is the permissive
	// REST fallback that would otherwise claim those paths.
	if d.Bedrock != nil {
		srv.Register(bedrock.New(d.Bedrock))
	}

	// bedrock-agent-runtime (InvokeAgent / Retrieve / RetrieveAndGenerate) shares
	// the /agents and /knowledgebases roots with the bedrock-agent control plane,
	// but matches only the runtime suffixes (/text, /retrieve) and
	// /retrieveAndGenerate. It MUST register before the control-plane handler so
	// its more specific Matches wins for those paths, and both before S3.
	if d.BedrockAgentRuntime != nil {
		srv.Register(bedrockagentruntime.New(d.BedrockAgentRuntime))
	}

	// bedrock-agent control plane: agents, knowledge bases, data sources, flows,
	// prompts. REST/JSON rooted at /agents, /knowledgebases, /flows, /prompts —
	// registered before S3's permissive REST fallback.
	if d.BedrockAgent != nil {
		srv.Register(bedrockagent.New(d.BedrockAgent))
	}

	// SageMaker control plane matches the X-Amz-Target prefix "SageMaker."
	// (disjoint from DynamoDB/SQS/Resource-Groups-Tagging), and the runtime
	// matches /endpoints/{name}/invocations. The runtime path must register
	// before S3's permissive REST fallback.
	if d.SageMaker != nil {
		srv.Register(sagemakersrv.New(d.SageMaker))
	}

	// Kubernetes data-plane API. Matches /k8s/{uid}/... — disjoint from
	// every other AWS path. Registered before S3's REST fallback.
	if d.K8sAPI != nil {
		srv.Register(d.K8sAPI)
	}

	// Resource Explorer 2 uses REST-JSON with fixed top-level paths
	// (/CreateView, /Search, etc.). Must register before S3's catch-all.
	if d.ResourceDiscovery != nil {
		srv.Register(resourceexplorer2.New(d.ResourceDiscovery, d.AccountID, d.Region))
	}

	// Route 53 is a REST/XML service rooted at /2013-04-01/hostedzone — its own
	// path space, disjoint from every other AWS handler. It must register
	// before S3 because S3 is the permissive REST fallback that would otherwise
	// claim those paths.
	if d.Route53 != nil {
		srv.Register(route53.New(d.Route53))
	}

	if d.S3 != nil {
		srv.Register(s3.New(d.S3))
	}

	return srv
}
