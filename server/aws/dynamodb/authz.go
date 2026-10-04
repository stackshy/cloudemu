package dynamodb

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// tableRef says where an operation names the resource it is authorized on.
type tableRef int

const (
	refNone          tableRef = iota // account-level, Resource "*"
	refTable                         // TableName
	refTableOrIndex                  // TableName, or its index when IndexName is set
	refResourceArn                   // ResourceArn, resolved to its table as dispatch does
	refBackup                        // BackupArn
	refRestoreBackup                 // BackupArn and TargetTableName
	refRestorePITR                   // SourceTableName and TargetTableName
	refGlobalTable                   // GlobalTableName
	refBatch                         // the tables keying RequestItems
	refTransactWrite                 // each TransactItems element's table, per kind
	refTransactGet                   // each TransactItems Get's table
)

// tableOps maps every operation ServeHTTP dispatches to where it names its
// resource, as listed under "Actions defined by Amazon DynamoDB". The IAM
// action is the operation name except for the transactions, which AWS
// authorizes as the item action of each element.
//
//nolint:gochecknoglobals,goconst // static lookup table of the operation names the dispatch routes list
var tableOps = map[string]tableRef{
	"CreateTable": refTable, "DeleteTable": refTable, "DescribeTable": refTable, "UpdateTable": refTable,
	"DescribeContinuousBackups": refTable, "UpdateContinuousBackups": refTable, "ListTables": refNone,
	"PutItem": refTable, "GetItem": refTable, "DeleteItem": refTable, "UpdateItem": refTable,
	"Query": refTableOrIndex, "Scan": refTableOrIndex,
	"BatchWriteItem": refBatch, "BatchGetItem": refBatch,
	"TransactWriteItems": refTransactWrite, "TransactGetItems": refTransactGet,
	"TagResource": refResourceArn, "UntagResource": refResourceArn, "ListTagsOfResource": refResourceArn,
	"DescribeTimeToLive": refTable, "UpdateTimeToLive": refTable,
	"CreateBackup": refTable, "DescribeBackup": refBackup, "DeleteBackup": refBackup, "ListBackups": refNone,
	"RestoreTableFromBackup": refRestoreBackup, "RestoreTableToPointInTime": refRestorePITR,
	"CreateGlobalTable": refGlobalTable, "DescribeGlobalTable": refGlobalTable, "UpdateGlobalTable": refGlobalTable,
	"ListGlobalTables":                    refNone,
	"DescribeKinesisStreamingDestination": refTable, "EnableKinesisStreamingDestination": refTable,
	"DisableKinesisStreamingDestination": refTable, "UpdateKinesisStreamingDestination": refTable,
	"DescribeContributorInsights": refTableOrIndex, "UpdateContributorInsights": refTableOrIndex,
	"ListContributorInsights": refNone,
	"DescribeLimits":          refNone, "DescribeEndpoints": refNone,
}

// transactWriteActions is the IAM action of each TransactWriteItems kind.
//
//nolint:gochecknoglobals // static lookup table
var transactWriteActions = map[string]string{
	"Put": "PutItem", "Update": "UpdateItem", "Delete": "DeleteItem", "ConditionCheck": "ConditionCheckItem",
}

var (
	// resourceName is the shape of a DynamoDB table or index name.
	resourceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,255}$`)
	// subResource is the table-relative part of a backup or stream ARN.
	subResource = regexp.MustCompile(`^table/[A-Za-z0-9_.-]{1,255}/(backup|stream)/[A-Za-z0-9:._-]{1,128}$`)
)

// IAMChecks names the IAM checks of a request from the X-Amz-Target ServeHTTP
// dispatches on, each on the table, index, backup or global table it runs
// on, in the server's account and region. Batch operations need the action
// on every table they touch and a transaction the item action of each
// element, one check per table. A resource the request does not name
// cleanly is evaluated as unknown. An operation the handler does not serve
// returns ok=false; ServeHTTP answers it with UnknownOperationException.
func (*Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	ref, ok := tableOps[op]
	if !ok {
		return nil, false
	}

	action := iamServicePrefix + ":" + op

	switch ref {
	case refNone:
		return awsauthz.Single(action, "*"), true
	case refBatch:
		return batchChecks(r, s, action), true
	case refTransactWrite:
		return transactWriteChecks(r, s), true
	case refTransactGet:
		return transactGetChecks(r, s), true
	case refTable, refTableOrIndex, refResourceArn, refBackup, refRestoreBackup, refRestorePITR, refGlobalTable:
	}

	return namedChecks(r, s, ref, action), true
}

// namedChecks is the check of an operation that names its resources in
// top-level request fields.
func namedChecks(r *http.Request, s awsauthz.Scope, ref tableRef, action string) []awsauthz.Check {
	var req struct {
		TableName       string `json:"TableName"`
		IndexName       string `json:"IndexName"`
		ResourceArn     string `json:"ResourceArn"`
		BackupArn       string `json:"BackupArn"`
		SourceTableName string `json:"SourceTableName"`
		TargetTableName string `json:"TargetTableName"`
		GlobalTableName string `json:"GlobalTableName"`
	}

	if !awsauthz.JSONBody(r, &req) {
		return awsauthz.Single(action, "")
	}

	switch ref {
	case refTableOrIndex:
		return awsauthz.Single(action, indexARN(s, req.TableName, req.IndexName))
	case refResourceArn:
		return awsauthz.Single(action, tableARN(s, tableFromARN(req.ResourceArn)))
	case refBackup:
		return awsauthz.Single(action, subResourceARN(s, req.BackupArn))
	case refRestoreBackup:
		return withAction(action, subResourceARN(s, req.BackupArn), tableARN(s, req.TargetTableName))
	case refRestorePITR:
		return withAction(action, tableARN(s, req.SourceTableName), tableARN(s, req.TargetTableName))
	case refGlobalTable:
		return awsauthz.Single(action, globalTableARN(s, req.GlobalTableName))
	case refNone, refTable, refBatch, refTransactWrite, refTransactGet:
	}

	return awsauthz.Single(action, tableARN(s, req.TableName))
}

// batchChecks is one check of action per table keying RequestItems, in name
// order. An empty or unreadable request is one check on an unknown resource.
func batchChecks(r *http.Request, s awsauthz.Scope, action string) []awsauthz.Check {
	var req struct {
		RequestItems map[string]json.RawMessage `json:"RequestItems"`
	}

	if !awsauthz.JSONBody(r, &req) || len(req.RequestItems) == 0 {
		return awsauthz.Single(action, "")
	}

	tables := make([]string, 0, len(req.RequestItems))
	for t := range req.RequestItems {
		tables = append(tables, tableARN(s, t))
	}

	sort.Strings(tables)

	return withAction(action, tables...)
}

// transactWriteChecks is the item action of each element on its table, as
// toTxOp picks the element's kind for dispatch, without repeats.
func transactWriteChecks(r *http.Request, s awsauthz.Scope) []awsauthz.Check {
	fallback := awsauthz.Single(iamServicePrefix+":PutItem", "")

	var req struct {
		TransactItems []transactWriteJSON `json:"TransactItems"`
	}

	if !awsauthz.JSONBody(r, &req) {
		return fallback
	}

	var checks []awsauthz.Check

	for _, op := range normalizeTransactItems(req.TransactItems) {
		checks = appendCheck(checks, iamServicePrefix+":"+transactWriteActions[op.kind], tableARN(s, op.table))
	}

	if len(checks) == 0 {
		return fallback
	}

	return checks
}

// transactGetChecks is dynamodb:GetItem on the table of each Get, without
// repeats.
func transactGetChecks(r *http.Request, s awsauthz.Scope) []awsauthz.Check {
	action := iamServicePrefix + ":GetItem"

	var req struct {
		TransactItems []struct {
			Get *struct {
				TableName string `json:"TableName"`
			} `json:"Get,omitempty"`
		} `json:"TransactItems"`
	}

	if !awsauthz.JSONBody(r, &req) {
		return awsauthz.Single(action, "")
	}

	var checks []awsauthz.Check

	for _, t := range req.TransactItems {
		if t.Get != nil {
			checks = appendCheck(checks, action, tableARN(s, t.Get.TableName))
		}
	}

	if len(checks) == 0 {
		return awsauthz.Single(action, "")
	}

	return checks
}

// appendCheck adds action on resource unless checks already holds it.
func appendCheck(checks []awsauthz.Check, action, resource string) []awsauthz.Check {
	c := awsauthz.Check{Action: action, Resource: resource}

	for _, have := range checks {
		if have == c {
			return checks
		}
	}

	return append(checks, c)
}

// withAction is one check of action per resource.
func withAction(action string, resources ...string) []awsauthz.Check {
	checks := make([]awsauthz.Check, 0, len(resources))
	for _, res := range resources {
		checks = append(checks, awsauthz.Check{Action: action, Resource: res})
	}

	return checks
}

// tableARN is the ARN of table name, or "" when name is not a table name.
func tableARN(s awsauthz.Scope, name string) string {
	if !resourceName.MatchString(name) {
		return ""
	}

	return s.ARN(iamServicePrefix, "table/"+name)
}

// indexARN is the ARN of the index when one is named, else of the table.
func indexARN(s awsauthz.Scope, table, index string) string {
	t := tableARN(s, table)
	if index == "" || t == "" {
		return t
	}

	if !resourceName.MatchString(index) {
		return ""
	}

	return t + "/index/" + index
}

// globalTableARN is the regionless ARN of global table name, or "".
func globalTableARN(s awsauthz.Scope, name string) string {
	if !resourceName.MatchString(name) {
		return ""
	}

	return s.GlobalARN(iamServicePrefix, "global-table/"+name)
}

// subResourceARN rebuilds a backup or stream ARN in the server's account and
// region from its table-relative part, or returns "" when arn is not one.
func subResourceARN(s awsauthz.Scope, arn string) string {
	const marker = ":table/"

	i := strings.Index(arn, marker)
	if !strings.HasPrefix(arn, "arn:") || i < 0 {
		return ""
	}

	res := arn[i+1:]
	if !subResource.MatchString(res) {
		return ""
	}

	return s.ARN(iamServicePrefix, res)
}

// IAMChecks names the IAM check of a DynamoDB Streams request from the
// X-Amz-Target ServeHTTP dispatches on. DescribeStream and GetShardIterator
// run on the stream they name; GetRecords on the stream of the table its
// shard iterator names, which is what the handler reads. ListStreams takes
// no resource. An operation the handler does not serve returns ok=false.
func (h *StreamsHandler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), streamsTargetPrefix)
	action := iamServicePrefix + ":" + op

	var req struct {
		StreamArn     string `json:"StreamArn"`
		ShardIterator string `json:"ShardIterator"`
	}

	//nolint:goconst // operation names, which the dispatch switch also lists
	switch op {
	case "ListStreams":
		return awsauthz.Single(action, "*"), true
	case "DescribeStream", "GetShardIterator":
		if !awsauthz.JSONBody(r, &req) {
			return awsauthz.Single(action, ""), true
		}

		return awsauthz.Single(action, subResourceARN(s, req.StreamArn)), true
	case "GetRecords":
		if !awsauthz.JSONBody(r, &req) {
			return awsauthz.Single(action, ""), true
		}

		return awsauthz.Single(action, h.iteratorStream(r, s, req.ShardIterator)), true
	default:
		return nil, false
	}
}

// iteratorStream is the stream ARN of the table a shard iterator names, or ""
// when the iterator does not decode or the table has no stream.
func (h *StreamsHandler) iteratorStream(r *http.Request, s awsauthz.Scope, iterator string) string {
	cur, err := decodeIterator(iterator)
	if err != nil {
		return ""
	}

	cfg, err := h.db.DescribeTable(r.Context(), cur.Table)
	if err != nil || !cfg.StreamEnabled {
		return ""
	}

	return subResourceARN(s, cfg.StreamArn)
}
