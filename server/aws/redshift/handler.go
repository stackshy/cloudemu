// Package redshift implements the AWS Redshift query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 Redshift client at a Server registered with this
// handler and Cluster/ClusterSnapshot operations work against an in-memory
// relationaldb driver.
//
// Redshift shares the AWS query wire shape with EC2 and RDS (POST + form-encoded
// body, XML response). To keep dispatch unambiguous, this handler's Matches
// predicate parses the form body once and only claims requests whose Action is
// one of the known Redshift operations. Register order matters: RDS first
// (DBInstance/DBCluster verbs), then Redshift (Cluster verbs), then EC2 as the
// catch-all. Each handler's action set is mutually exclusive.
package redshift

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// Namespace is the XML namespace for AWS Redshift responses.
const Namespace = "http://redshift.amazonaws.com/doc/2012-12-01/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// redshiftActions is the set of Action values this handler recognizes. Matches
// uses it to decide whether to claim a request.
var redshiftActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateCluster":              {},
	"DescribeClusters":           {},
	"ModifyCluster":              {},
	"DeleteCluster":              {},
	"RebootCluster":              {},
	"CreateClusterSnapshot":      {},
	"DescribeClusterSnapshots":   {},
	"DeleteClusterSnapshot":      {},
	"RestoreFromClusterSnapshot": {},
}

// Handler serves Redshift query-protocol requests.
type Handler struct {
	db rdbdriver.RelationalDB
}

// New returns a Redshift handler backed by db.
func New(db rdbdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches returns true if the request looks like an AWS Redshift query-protocol
// call (POST + form-encoded body whose Action is one of the known Redshift
// operations). Calling ParseForm here caches the parsed form on the request so
// ServeHTTP can use it without re-reading the body.
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

	_, ok := redshiftActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateCluster":
		h.createCluster(w, r)
	case "DescribeClusters":
		h.describeClusters(w, r)
	case "ModifyCluster":
		h.modifyCluster(w, r)
	case "DeleteCluster":
		h.deleteCluster(w, r)
	case "RebootCluster":
		h.rebootCluster(w, r)
	case "CreateClusterSnapshot":
		h.createClusterSnapshot(w, r)
	case "DescribeClusterSnapshots":
		h.describeClusterSnapshots(w, r)
	case "DeleteClusterSnapshot":
		h.deleteClusterSnapshot(w, r)
	case "RestoreFromClusterSnapshot":
		h.restoreFromClusterSnapshot(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown Redshift action: "+action)
	}
}

// writeErr maps cloudemu errors to Redshift XML error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, notFoundCode(err), err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, alreadyExistsCode(err), err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidClusterState", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
	}
}

// notFoundCode picks the AWS-shaped error code based on the error message.
func notFoundCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "cluster snapshot"):
		return "ClusterSnapshotNotFound"
	case strings.Contains(msg, "cluster"):
		return "ClusterNotFound"
	default:
		return "ResourceNotFoundFault"
	}
}

func alreadyExistsCode(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "cluster snapshot"):
		return "ClusterSnapshotAlreadyExists"
	case strings.Contains(msg, "cluster"):
		return "ClusterAlreadyExists"
	default:
		return "ResourceAlreadyExistsFault"
	}
}
