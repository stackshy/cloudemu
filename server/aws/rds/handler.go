// Package rds implements the AWS RDS query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 RDS client at a Server registered with this
// handler and DBInstance/DBCluster/DBSnapshot operations work against an
// in-memory relationaldb driver.
//
// RDS shares the AWS query wire shape with EC2 (POST + form-encoded body, XML
// response). To keep dispatch unambiguous, this handler's Matches predicate
// parses the form body once and only claims requests whose Action is one of
// the known RDS operations. The EC2 handler is the catch-all for all other
// query-protocol actions and so this handler MUST register first.
package rds

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// Namespace is the XML namespace for AWS RDS responses.
const Namespace = "http://rds.amazonaws.com/doc/2014-10-31/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// rdsActions is the set of Action values this handler recognizes. Matches uses
// it to decide whether to claim a request.
var rdsActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateDBInstance":                {},
	"DescribeDBInstances":             {},
	"ModifyDBInstance":                {},
	"DeleteDBInstance":                {},
	"StartDBInstance":                 {},
	"StopDBInstance":                  {},
	"RebootDBInstance":                {},
	"CreateDBCluster":                 {},
	"DescribeDBClusters":              {},
	"ModifyDBCluster":                 {},
	"DeleteDBCluster":                 {},
	"StartDBCluster":                  {},
	"StopDBCluster":                   {},
	"CreateDBSnapshot":                {},
	"DescribeDBSnapshots":             {},
	"DeleteDBSnapshot":                {},
	"RestoreDBInstanceFromDBSnapshot": {},
	"CreateDBClusterSnapshot":         {},
	"DescribeDBClusterSnapshots":      {},
	"DeleteDBClusterSnapshot":         {},
	"RestoreDBClusterFromSnapshot":    {},
}

// Handler serves RDS query-protocol requests.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns an RDS handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true if the request looks like an AWS RDS query-protocol
// call (POST + form-encoded body whose Action is one of the known RDS
// operations). Calling ParseForm here caches the parsed form on the request
// so ServeHTTP can use it without re-reading the body.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	_, ok := rdsActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
//
//nolint:gocyclo // 21 cases for one-shot dispatch; splitting into sub-routers would be more complex than the switch.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateDBInstance":
		h.createDBInstance(w, r)
	case "DescribeDBInstances":
		h.describeDBInstances(w, r)
	case "ModifyDBInstance":
		h.modifyDBInstance(w, r)
	case "DeleteDBInstance":
		h.deleteDBInstance(w, r)
	case "StartDBInstance":
		h.startDBInstance(w, r)
	case "StopDBInstance":
		h.stopDBInstance(w, r)
	case "RebootDBInstance":
		h.rebootDBInstance(w, r)
	case "CreateDBCluster":
		h.createDBCluster(w, r)
	case "DescribeDBClusters":
		h.describeDBClusters(w, r)
	case "ModifyDBCluster":
		h.modifyDBCluster(w, r)
	case "DeleteDBCluster":
		h.deleteDBCluster(w, r)
	case "StartDBCluster":
		h.startDBCluster(w, r)
	case "StopDBCluster":
		h.stopDBCluster(w, r)
	case "CreateDBSnapshot":
		h.createDBSnapshot(w, r)
	case "DescribeDBSnapshots":
		h.describeDBSnapshots(w, r)
	case "DeleteDBSnapshot":
		h.deleteDBSnapshot(w, r)
	case "RestoreDBInstanceFromDBSnapshot":
		h.restoreInstanceFromSnapshot(w, r)
	case "CreateDBClusterSnapshot":
		h.createDBClusterSnapshot(w, r)
	case "DescribeDBClusterSnapshots":
		h.describeDBClusterSnapshots(w, r)
	case "DeleteDBClusterSnapshot":
		h.deleteDBClusterSnapshot(w, r)
	case "RestoreDBClusterFromSnapshot":
		h.restoreClusterFromSnapshot(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown RDS action: "+action)
	}
}

// writeErr maps cloudemu errors to RDS XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, notFoundCode(err), err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, alreadyExistsCode(err), err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidDBInstanceState", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}

// notFoundCode picks the AWS-shaped error code based on the error message.
// We can't introspect the resource type from cerrors directly; the message
// always carries the resource keyword.
func notFoundCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "DB instance"):
		return "DBInstanceNotFound"
	case strings.Contains(msg, "DB cluster snapshot"):
		return "DBClusterSnapshotNotFoundFault"
	case strings.Contains(msg, "DB cluster"):
		return "DBClusterNotFoundFault"
	case strings.Contains(msg, "DB snapshot"):
		return "DBSnapshotNotFound"
	default:
		return "ResourceNotFoundFault"
	}
}

func alreadyExistsCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "DB instance"):
		return "DBInstanceAlreadyExists"
	case strings.Contains(msg, "DB cluster snapshot"):
		return "DBClusterSnapshotAlreadyExistsFault"
	case strings.Contains(msg, "DB cluster"):
		return "DBClusterAlreadyExistsFault"
	case strings.Contains(msg, "DB snapshot"):
		return "DBSnapshotAlreadyExists"
	default:
		return "ResourceAlreadyExistsFault"
	}
}
