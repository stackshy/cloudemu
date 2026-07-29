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

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
	"CreateDBSubnetGroup":                {},
	"DescribeDBSubnetGroups":             {},
	"DeleteDBSubnetGroup":                {},
	"CreateDBInstance":                   {},
	"DescribeDBInstances":                {},
	"ModifyDBInstance":                   {},
	"DeleteDBInstance":                   {},
	"StartDBInstance":                    {},
	"StopDBInstance":                     {},
	"RebootDBInstance":                   {},
	"CreateDBCluster":                    {},
	"DescribeDBClusters":                 {},
	"ModifyDBCluster":                    {},
	"DeleteDBCluster":                    {},
	"StartDBCluster":                     {},
	"StopDBCluster":                      {},
	"CreateDBSnapshot":                   {},
	"DescribeDBSnapshots":                {},
	"DeleteDBSnapshot":                   {},
	"RestoreDBInstanceFromDBSnapshot":    {},
	"CreateDBClusterSnapshot":            {},
	"DescribeDBClusterSnapshots":         {},
	"DeleteDBClusterSnapshot":            {},
	"RestoreDBClusterFromSnapshot":       {},
	"CreateDBParameterGroup":             {},
	"DescribeDBParameterGroups":          {},
	"ModifyDBParameterGroup":             {},
	"DeleteDBParameterGroup":             {},
	"DescribeDBParameters":               {},
	"ResetDBParameterGroup":              {},
	"CopyDBParameterGroup":               {},
	"CreateDBClusterParameterGroup":      {},
	"DescribeDBClusterParameterGroups":   {},
	"ModifyDBClusterParameterGroup":      {},
	"DeleteDBClusterParameterGroup":      {},
	"DescribeDBClusterParameters":        {},
	"ResetDBClusterParameterGroup":       {},
	"CopyDBClusterParameterGroup":        {},
	"CreateOptionGroup":                  {},
	"DescribeOptionGroups":               {},
	"ModifyOptionGroup":                  {},
	"DeleteOptionGroup":                  {},
	"CopyOptionGroup":                    {},
	"DescribeOptionGroupOptions":         {},
	"CreateDBInstanceReadReplica":        {},
	"PromoteReadReplica":                 {},
	"CopyDBSnapshot":                     {},
	"CopyDBClusterSnapshot":              {},
	"RestoreDBInstanceToPointInTime":     {},
	"RestoreDBClusterToPointInTime":      {},
	"CreateDBProxy":                      {},
	"DescribeDBProxies":                  {},
	"ModifyDBProxy":                      {},
	"DeleteDBProxy":                      {},
	"RegisterDBProxyTargets":             {},
	"DeregisterDBProxyTargets":           {},
	"DescribeDBProxyTargets":             {},
	"DescribeDBProxyTargetGroups":        {},
	"CreateEventSubscription":            {},
	"DescribeEventSubscriptions":         {},
	"ModifyEventSubscription":            {},
	"DeleteEventSubscription":            {},
	"DescribeEvents":                     {},
	"DescribeEventCategories":            {},
	"CreateDBClusterEndpoint":            {},
	"DescribeDBClusterEndpoints":         {},
	"ModifyDBClusterEndpoint":            {},
	"DeleteDBClusterEndpoint":            {},
	"FailoverDBCluster":                  {},
	"CreateGlobalCluster":                {},
	"DescribeGlobalClusters":             {},
	"ModifyGlobalCluster":                {},
	"DeleteGlobalCluster":                {},
	"RemoveFromGlobalCluster":            {},
	"DescribeDBEngineVersions":           {},
	"DescribeOrderableDBInstanceOptions": {},
	"AddTagsToResource":                  {},
	"RemoveTagsFromResource":             {},
	"ListTagsForResource":                {},
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
//nolint:gocyclo,funlen // flat one-shot Action dispatch; a table of func values would obscure it more than the switch.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateDBSubnetGroup":
		h.createDBSubnetGroup(w, r)
	case "DescribeDBSubnetGroups":
		h.describeDBSubnetGroups(w, r)
	case "DeleteDBSubnetGroup":
		h.deleteDBSubnetGroup(w, r)
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
	case "CreateDBParameterGroup":
		h.createDBParameterGroup(w, r)
	case "DescribeDBParameterGroups":
		h.describeDBParameterGroups(w, r)
	case "ModifyDBParameterGroup":
		h.modifyDBParameterGroup(w, r)
	case "DeleteDBParameterGroup":
		h.deleteDBParameterGroup(w, r)
	case "DescribeDBParameters":
		h.describeDBParameters(w, r)
	case "ResetDBParameterGroup":
		h.resetDBParameterGroup(w, r)
	case "CopyDBParameterGroup":
		h.copyDBParameterGroup(w, r)
	case "CreateDBClusterParameterGroup":
		h.createDBClusterParameterGroup(w, r)
	case "DescribeDBClusterParameterGroups":
		h.describeDBClusterParameterGroups(w, r)
	case "ModifyDBClusterParameterGroup":
		h.modifyDBClusterParameterGroup(w, r)
	case "DeleteDBClusterParameterGroup":
		h.deleteDBClusterParameterGroup(w, r)
	case "DescribeDBClusterParameters":
		h.describeDBClusterParameters(w, r)
	case "ResetDBClusterParameterGroup":
		h.resetDBClusterParameterGroup(w, r)
	case "CopyDBClusterParameterGroup":
		h.copyDBClusterParameterGroup(w, r)
	case "CreateOptionGroup":
		h.createOptionGroup(w, r)
	case "DescribeOptionGroups":
		h.describeOptionGroups(w, r)
	case "ModifyOptionGroup":
		h.modifyOptionGroup(w, r)
	case "DeleteOptionGroup":
		h.deleteOptionGroup(w, r)
	case "CopyOptionGroup":
		h.copyOptionGroup(w, r)
	case "DescribeOptionGroupOptions":
		h.describeOptionGroupOptions(w, r)
	case "CreateDBInstanceReadReplica":
		h.createDBInstanceReadReplica(w, r)
	case "PromoteReadReplica":
		h.promoteReadReplica(w, r)
	case "CopyDBSnapshot":
		h.copyDBSnapshot(w, r)
	case "CopyDBClusterSnapshot":
		h.copyDBClusterSnapshot(w, r)
	case "RestoreDBInstanceToPointInTime":
		h.restoreDBInstanceToPointInTime(w, r)
	case "RestoreDBClusterToPointInTime":
		h.restoreDBClusterToPointInTime(w, r)
	case "CreateDBProxy":
		h.createDBProxy(w, r)
	case "DescribeDBProxies":
		h.describeDBProxies(w, r)
	case "ModifyDBProxy":
		h.modifyDBProxy(w, r)
	case "DeleteDBProxy":
		h.deleteDBProxy(w, r)
	case "RegisterDBProxyTargets":
		h.registerDBProxyTargets(w, r)
	case "DeregisterDBProxyTargets":
		h.deregisterDBProxyTargets(w, r)
	case "DescribeDBProxyTargets":
		h.describeDBProxyTargets(w, r)
	case "DescribeDBProxyTargetGroups":
		h.describeDBProxyTargetGroups(w, r)
	case "CreateEventSubscription":
		h.createEventSubscription(w, r)
	case "DescribeEventSubscriptions":
		h.describeEventSubscriptions(w, r)
	case "ModifyEventSubscription":
		h.modifyEventSubscription(w, r)
	case "DeleteEventSubscription":
		h.deleteEventSubscription(w, r)
	case "DescribeEvents":
		h.describeEvents(w, r)
	case "DescribeEventCategories":
		h.describeEventCategories(w, r)
	case "CreateDBClusterEndpoint":
		h.createDBClusterEndpoint(w, r)
	case "DescribeDBClusterEndpoints":
		h.describeDBClusterEndpoints(w, r)
	case "ModifyDBClusterEndpoint":
		h.modifyDBClusterEndpoint(w, r)
	case "DeleteDBClusterEndpoint":
		h.deleteDBClusterEndpoint(w, r)
	case "FailoverDBCluster":
		h.failoverDBCluster(w, r)
	case "CreateGlobalCluster":
		h.createGlobalCluster(w, r)
	case "DescribeGlobalClusters":
		h.describeGlobalClusters(w, r)
	case "ModifyGlobalCluster":
		h.modifyGlobalCluster(w, r)
	case "DeleteGlobalCluster":
		h.deleteGlobalCluster(w, r)
	case "RemoveFromGlobalCluster":
		h.removeFromGlobalCluster(w, r)
	case "DescribeDBEngineVersions":
		h.describeDBEngineVersions(w, r)
	case "DescribeOrderableDBInstanceOptions":
		h.describeOrderableDBInstanceOptions(w, r)
	case "AddTagsToResource":
		h.addTagsToResource(w, r)
	case "RemoveTagsFromResource":
		h.removeTagsFromResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
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

// faultMapping maps a resource keyword found in an error message to the
// AWS-shaped fault code for that resource.
type faultMapping struct {
	substr string
	code   string
}

// matchFault returns the code of the first mapping whose keyword is contained
// in msg, or fallback if none match. The caller supplies the table in
// most-specific-first order (e.g. "DB cluster endpoint" and "parameter group"
// before "DB cluster", whose keyword they contain).
func matchFault(msg string, table []faultMapping, fallback string) string {
	for _, m := range table {
		if strings.Contains(msg, m.substr) {
			return m.code
		}
	}

	return fallback
}

// notFoundFaults / alreadyExistsFaults are ordered most-specific-first: an
// entry whose keyword is a substring of another's must appear first. cerrors
// carries no resource type, so the resource keyword in the message is the only
// signal for the AWS-shaped code. Real AWS reuses the DBParameterGroup fault
// for both DB and cluster parameter groups.
//
//nolint:gochecknoglobals // ordered static lookup table
var notFoundFaults = []faultMapping{
	{"db subnet group", "DBSubnetGroupNotFoundFault"},
	{"parameter group", "DBParameterGroupNotFound"},
	{"option group", "OptionGroupNotFoundFault"},
	{"DB proxy", "DBProxyNotFoundFault"},
	{"event subscription", "SubscriptionNotFoundFault"},
	{"DB cluster endpoint", "DBClusterEndpointNotFoundFault"},
	{"global cluster", "GlobalClusterNotFoundFault"},
	{"DB instance", "DBInstanceNotFound"},
	{"DB cluster snapshot", "DBClusterSnapshotNotFoundFault"},
	{"DB cluster", "DBClusterNotFoundFault"},
	{"DB snapshot", "DBSnapshotNotFound"},
}

//nolint:gochecknoglobals // ordered static lookup table
var alreadyExistsFaults = []faultMapping{
	{"db subnet group", "DBSubnetGroupAlreadyExists"},
	{"parameter group", "DBParameterGroupAlreadyExists"},
	{"option group", "OptionGroupAlreadyExistsFault"},
	{"DB proxy", "DBProxyAlreadyExistsFault"},
	{"event subscription", "SubscriptionAlreadyExistFault"},
	{"DB cluster endpoint", "DBClusterEndpointAlreadyExistsFault"},
	{"global cluster", "GlobalClusterAlreadyExistsFault"},
	{"DB instance", "DBInstanceAlreadyExists"},
	{"DB cluster snapshot", "DBClusterSnapshotAlreadyExistsFault"},
	{"DB cluster", "DBClusterAlreadyExistsFault"},
	{"DB snapshot", "DBSnapshotAlreadyExists"},
}

// notFoundCode picks the AWS-shaped NotFound fault from the error message.
func notFoundCode(err error) string {
	return matchFault(err.Error(), notFoundFaults, "ResourceNotFoundFault")
}

// alreadyExistsCode picks the AWS-shaped AlreadyExists fault from the message.
func alreadyExistsCode(err error) string {
	return matchFault(err.Error(), alreadyExistsFaults, "ResourceAlreadyExistsFault")
}
