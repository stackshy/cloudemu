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
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	redshiftprovider "github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
	"CreateCluster":                  {},
	"DescribeClusters":               {},
	"ModifyCluster":                  {},
	"DeleteCluster":                  {},
	"RebootCluster":                  {},
	"CreateClusterSnapshot":          {},
	"DescribeClusterSnapshots":       {},
	"DeleteClusterSnapshot":          {},
	"RestoreFromClusterSnapshot":     {},
	"GetClusterCredentials":          {},
	"PauseCluster":                   {},
	"ResumeCluster":                  {},
	"CreateClusterParameterGroup":    {},
	"DescribeClusterParameterGroups": {},
	"DeleteClusterParameterGroup":    {},
	"ModifyClusterParameterGroup":    {},
	"DescribeClusterParameters":      {},
	"ResetClusterParameterGroup":     {},
	"CreateClusterSubnetGroup":       {},
	"DescribeClusterSubnetGroups":    {},
	"DeleteClusterSubnetGroup":       {},
	"CreateTags":                     {},
	"DeleteTags":                     {},
	"DescribeTags":                   {},
}

// clusterGroupManager is the AWS-specific parameter/subnet-group surface, not
// part of the shared relationaldb driver; the handler type-asserts for it.
type clusterGroupManager interface {
	CreateClusterParameterGroup(ctx context.Context, name, family, description string) (*redshiftprovider.ParameterGroup, error)
	DescribeClusterParameterGroups(ctx context.Context, names []string) ([]redshiftprovider.ParameterGroup, error)
	DeleteClusterParameterGroup(ctx context.Context, name string) error
	ModifyClusterParameterGroup(
		ctx context.Context, name string, params []rdbdriver.Parameter,
	) (*redshiftprovider.ParameterGroup, error)
	DescribeClusterParameters(ctx context.Context, name string) ([]rdbdriver.Parameter, error)
	ResetClusterParameterGroup(
		ctx context.Context, name string, paramNames []string, resetAll bool,
	) (*redshiftprovider.ParameterGroup, error)
	CreateClusterSubnetGroup(ctx context.Context, name, description string, subnetIDs []string) (*redshiftprovider.SubnetGroup, error)
	DescribeClusterSubnetGroups(ctx context.Context, names []string) ([]redshiftprovider.SubnetGroup, error)
	DeleteClusterSubnetGroup(ctx context.Context, name string) error
}

// clusterPauser is the AWS-only PauseCluster/ResumeCluster surface, kept off
// the shared relationaldb driver; the handler discovers it by type assertion
// and answers InvalidAction when the backing driver does not implement it.
type clusterPauser interface {
	PauseCluster(ctx context.Context, id string) (*rdbdriver.Cluster, error)
	ResumeCluster(ctx context.Context, id string) (*rdbdriver.Cluster, error)
}

// resourceTagger is the AWS-specific Redshift tagging surface.
type resourceTagger interface {
	CreateTags(ctx context.Context, resourceName string, tags map[string]string) error
	DeleteTags(ctx context.Context, resourceName string, keys []string) error
	DescribeTags(ctx context.Context, resourceName string) (map[string]string, error)
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

	action := r.Form.Get("Action")
	if _, ok := redshiftActions[action]; !ok {
		return false
	}

	// CreateTags/DeleteTags/DescribeTags are generic tag verbs shared with EC2
	// and ELBv2 on the same query protocol. Redshift registers before both, so
	// claim these only when the SigV4 credential scope names "redshift";
	// otherwise let them fall through to the owning handler.
	if _, ambiguous := ambiguousTagActions[action]; ambiguous {
		return awsquery.CredentialScopeService(r.Header.Get("Authorization")) == "redshift"
	}

	return true
}

// ambiguousTagActions are the tag verbs Redshift shares with other
// query-protocol services (EC2 CreateTags/DeleteTags, ELBv2 DescribeTags).
var ambiguousTagActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateTags":   {},
	"DeleteTags":   {},
	"DescribeTags": {},
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
	case "GetClusterCredentials":
		h.getClusterCredentials(w, r)
	case "PauseCluster":
		h.pauseCluster(w, r)
	case "ResumeCluster":
		h.resumeCluster(w, r)
	case "CreateClusterParameterGroup":
		h.createClusterParameterGroup(w, r)
	case "DescribeClusterParameterGroups":
		h.describeClusterParameterGroups(w, r)
	case "DeleteClusterParameterGroup":
		h.deleteClusterParameterGroup(w, r)
	case "ModifyClusterParameterGroup":
		h.modifyClusterParameterGroup(w, r)
	case "DescribeClusterParameters":
		h.describeClusterParameters(w, r)
	case "ResetClusterParameterGroup":
		h.resetClusterParameterGroup(w, r)
	case "CreateClusterSubnetGroup":
		h.createClusterSubnetGroup(w, r)
	case "DescribeClusterSubnetGroups":
		h.describeClusterSubnetGroups(w, r)
	case "DeleteClusterSubnetGroup":
		h.deleteClusterSubnetGroup(w, r)
	case "CreateTags":
		h.createTags(w, r)
	case "DeleteTags":
		h.deleteTags(w, r)
	case "DescribeTags":
		h.describeTags(w, r)
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
	case strings.Contains(msg, "parameter group"):
		return "ClusterParameterGroupNotFound"
	case strings.Contains(msg, "subnet group"):
		return "ClusterSubnetGroupNotFoundFault"
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
