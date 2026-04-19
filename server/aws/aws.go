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
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/server/aws/ec2"
	"github.com/stackshy/cloudemu/server/aws/s3"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers bundles the driver interfaces the AWS server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
type Drivers struct {
	S3       storagedriver.Bucket
	DynamoDB dbdriver.Database
	EC2      computedriver.Compute
	VPC      netdriver.Networking
}

// New returns a server that speaks the AWS SDK wire protocols for every
// non-nil driver in d. Handlers are registered most-specific-first so the
// dispatch is unambiguous:
//
//   - DynamoDB matches on X-Amz-Target header (JSON-RPC).
//   - EC2 matches on Action= (form-encoded POST or query string). The EC2
//     handler also serves VPC/networking ops since real AWS uses one endpoint
//     for both.
//   - S3 is the REST fallback.
func New(d Drivers) *server.Server {
	srv := server.New()

	if d.DynamoDB != nil {
		srv.Register(dynamodb.New(d.DynamoDB))
	}

	if d.EC2 != nil || d.VPC != nil {
		srv.Register(ec2.New(d.EC2, d.VPC))
	}

	if d.S3 != nil {
		srv.Register(s3.New(d.S3))
	}

	return srv
}
