// Package aws assembles CloudEmu's AWS-compatible HTTP server.
//
// New returns a *server.Server preloaded with handlers for each driver
// provided. Pass nil for a driver to leave that service unexposed. Consumers
// that want a single service can skip this package and register the handler
// directly on their own server.Server.
package aws

import (
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/server/aws/s3"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// New returns a server that speaks the AWS SDK wire protocols for the given
// drivers. Handlers are registered most-specific-first so dispatch is
// unambiguous.
func New(storage storagedriver.Bucket, database dbdriver.Database) *server.Server {
	srv := server.New()

	// DynamoDB is the more specific matcher (header-based), so register first.
	if database != nil {
		srv.Register(dynamodb.New(database))
	}

	if storage != nil {
		srv.Register(s3.New(storage))
	}

	return srv
}
