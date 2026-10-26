package elasticache

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ecprovider "github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// awsFaulter is an error that names its ElastiCache wire fault code. The
// provider's typed Fault implements it.
type awsFaulter interface {
	AWSFault() string
}

// faultStatus is the HTTP status ElastiCache answers each fault code with, from
// the botocore model (error.httpStatusCode). Note that CacheSubnetGroupNotFound
// is a 400 in the real API, unlike most not-found faults.
func faultStatus(code string) int {
	switch code {
	case ecprovider.FaultCacheClusterNotFound,
		ecprovider.FaultReplicationGroupNotFound,
		ecprovider.FaultCacheParameterGroupNotFound,
		ecprovider.FaultSnapshotNotFound:
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

// writeErr maps cloudemu errors to ElastiCache XML error responses.
//
// A typed fault carries its wire code; the message is written without any code
// token, since a real ElastiCache <Message> never begins with one. Errors with
// no fault code (request parsing, unsupported capabilities) fall back to a
// code chosen by the canonical error kind.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var f awsFaulter
	if errors.As(err, &f) {
		awsquery.WriteXMLError(w, faultStatus(f.AWSFault()), f.AWSFault(), msg)
		return
	}

	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, ecprovider.FaultCacheClusterNotFound, msg)
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, ecprovider.FaultCacheClusterAlreadyExists, msg)
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", msg)
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, ecprovider.FaultInvalidCacheClusterState, msg)
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}
