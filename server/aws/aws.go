// Package aws assembles CloudEmu's AWS-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package aws

import (
	bedrockdriver "github.com/stackshy/cloudemu/bedrock/driver"
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/resourcediscovery"
	sagemakerdriver "github.com/stackshy/cloudemu/sagemaker/driver"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/aws/bedrock"
	"github.com/stackshy/cloudemu/server/aws/cloudwatch"
	"github.com/stackshy/cloudemu/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/server/aws/ec2"
	"github.com/stackshy/cloudemu/server/aws/ecr"
	"github.com/stackshy/cloudemu/server/aws/eks"
	"github.com/stackshy/cloudemu/server/aws/elbv2"
	"github.com/stackshy/cloudemu/server/aws/iam"
	"github.com/stackshy/cloudemu/server/aws/lambda"
	"github.com/stackshy/cloudemu/server/aws/rds"
	"github.com/stackshy/cloudemu/server/aws/redshift"
	"github.com/stackshy/cloudemu/server/aws/resourceexplorer2"
	"github.com/stackshy/cloudemu/server/aws/resourcegroupstaggingapi"
	"github.com/stackshy/cloudemu/server/aws/route53"
	"github.com/stackshy/cloudemu/server/aws/s3"
	sagemakersrv "github.com/stackshy/cloudemu/server/aws/sagemaker"
	secretsmanagersrv "github.com/stackshy/cloudemu/server/aws/secretsmanager"
	"github.com/stackshy/cloudemu/server/aws/sqs"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers bundles the driver interfaces the AWS server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
type Drivers struct {
	S3         storagedriver.Bucket
	DynamoDB   dbdriver.Database
	EC2        computedriver.Compute
	VPC        netdriver.Networking
	CloudWatch mondriver.Monitoring
	Lambda     sdrv.Serverless
	SQS        mqdriver.MessageQueue
	RDS        rdbdriver.RelationalDB
	Redshift   rdbdriver.RelationalDB
	EKS        eksdriver.EKS
	IAM        iamdriver.IAM
	ECR        crdriver.ContainerRegistry
	Bedrock    bedrockdriver.Bedrock
	SageMaker  sagemakerdriver.Service
	// SecretsManager serves the Secrets Manager JSON 1.1 protocol against
	// the secrets driver.
	SecretsManager secretsdriver.Secrets
	// Route53 serves the Route 53 REST/XML protocol against the dns driver.
	Route53 dnsdriver.DNS
	// ELB serves the Elastic Load Balancing v2 (ALB/NLB) query protocol
	// against the loadbalancer driver.
	ELB lbdriver.LoadBalancer
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
//nolint:gocritic,gocyclo // Drivers is by-value for ergonomics; the dispatch is one if-per-driver and grows with the bundle.
func New(d Drivers) *server.Server {
	srv := server.New()

	if d.CloudWatch != nil {
		srv.Register(cloudwatch.New(d.CloudWatch))
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

	if d.EC2 != nil || d.VPC != nil {
		srv.Register(ec2.New(d.EC2, d.VPC))
	}

	if d.Lambda != nil {
		srv.Register(lambda.New(d.Lambda))
	}

	// EKS is a REST/JSON service rooted at /clusters. It must register
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
