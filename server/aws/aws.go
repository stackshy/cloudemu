// Package aws assembles CloudEmu's AWS-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package aws

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/aws/cloudwatch"
	"github.com/stackshy/cloudemu/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/server/aws/ec2"
	"github.com/stackshy/cloudemu/server/aws/eks"
	"github.com/stackshy/cloudemu/server/aws/lambda"
	"github.com/stackshy/cloudemu/server/aws/rds"
	"github.com/stackshy/cloudemu/server/aws/redshift"
	"github.com/stackshy/cloudemu/server/aws/s3"
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

	// RDS must be registered before EC2: both speak AWS query-protocol on
	// POST + form-encoded bodies, and Server matches in registration order.
	// RDS's Matches is action-specific, so a request bound for EC2 will fall
	// through to the EC2 handler unchanged.
	if d.RDS != nil {
		srv.Register(rds.New(d.RDS))
	}

	// Redshift sits between RDS and EC2 in the query-protocol pecking order.
	// Its action set (CreateCluster, DescribeClusters, …) is disjoint from
	// RDS's (CreateDBInstance, …) and from EC2's (RunInstances, …), so no
	// shadowing occurs.
	if d.Redshift != nil {
		srv.Register(redshift.New(d.Redshift))
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

	if d.S3 != nil {
		srv.Register(s3.New(d.S3))
	}

	return srv
}
