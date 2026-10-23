package cloudwatchlogs

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// The messages CreateExportTask returns when the destination bucket can't be
// used. All are InvalidParameterException.
const (
	bucketMissingMsg = "The given bucket does not exist. Please make sure the bucket is valid."
	grantCheckMsg    = " call on the given bucket failed. " +
		"Please check if CloudWatch Logs has been granted permission to perform this operation."
	getBucketACLMsg    = "GetBucketAcl" + grantCheckMsg
	putObjectFailedMsg = "PutObject" + grantCheckMsg + " This may be a result of a KMS key or bucket encryption misconfiguration."

	// exportWriteTestKey and its body are the permission-check object
	// CloudWatch Logs writes under the prefix when a task is created.
	exportWriteTestKey  = "aws-logs-write-test"
	exportWriteTestBody = "Permission Check Successful"

	bucketPolicyConfig  = "policy"
	logsGlobalPrincipal = "logs.amazonaws.com"
	logsServicePrefix   = "logs."
	amazonawsSuffix     = ".amazonaws.com"
)

// ExportStore is the part of S3 export tasks use: bucket existence, the
// bucket policy (the raw document the wire API stores, or the typed one the
// Go API stores) and object writes. The S3 mock satisfies it.
type ExportStore interface {
	ListBuckets(ctx context.Context) ([]storagedriver.BucketInfo, error)
	GetBucketConfig(ctx context.Context, bucket, name string) ([]byte, error)
	GetBucketPolicy(ctx context.Context, bucket string) (*storagedriver.BucketPolicy, error)
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error
}

// SetExportStore wires S3 in as the destination of export tasks. Without it
// every CreateExportTask fails as if the bucket did not exist.
func (m *Mock) SetExportStore(s ExportStore) {
	m.exportStore = s
}

// checkDestination does what CloudWatch Logs does before it accepts a task:
// the bucket must exist, its policy must let the logs service principal call
// GetBucketAcl on it and PutObject under the prefix, and the permission-check
// object must be written. Policy conditions are not evaluated.
func (m *Mock) checkDestination(ctx context.Context, bucket, prefix string) error {
	if m.exportStore == nil || !m.bucketExists(ctx, bucket) {
		return cerrors.New(cerrors.InvalidArgument, bucketMissingMsg)
	}

	stmts := m.bucketPolicy(ctx, bucket)
	bucketARN := "arn:aws:s3:::" + bucket
	testKey := path.Join(prefix, exportWriteTestKey)

	if !logsAllowed(stmts, "s3:GetBucketAcl", bucketARN) {
		return cerrors.New(cerrors.InvalidArgument, getBucketACLMsg)
	}

	if !logsAllowed(stmts, "s3:PutObject", bucketARN+"/"+testKey) {
		return cerrors.New(cerrors.InvalidArgument, putObjectFailedMsg)
	}

	if err := m.exportStore.PutObject(ctx, bucket, testKey, []byte(exportWriteTestBody), "text/plain", nil); err != nil {
		return cerrors.New(cerrors.InvalidArgument, putObjectFailedMsg)
	}

	return nil
}

func (m *Mock) bucketExists(ctx context.Context, bucket string) bool {
	buckets, err := m.exportStore.ListBuckets(ctx)
	if err != nil {
		return false
	}

	for i := range buckets {
		if buckets[i].Name == bucket {
			return true
		}
	}

	return false
}

// policyStatement is the part of a bucket policy statement the check reads.
// Principal, Action and Resource are a string or a list in the document.
type policyStatement struct {
	Effect    string          `json:"Effect"`
	Principal json.RawMessage `json:"Principal"`
	Action    json.RawMessage `json:"Action"`
	Resource  json.RawMessage `json:"Resource"`

	principals, actions, resources []string
}

// bucketPolicy returns the bucket's policy statements: the document stored by
// PutBucketPolicy over the wire, else the typed policy, else none.
func (m *Mock) bucketPolicy(ctx context.Context, bucket string) []policyStatement {
	if raw, err := m.exportStore.GetBucketConfig(ctx, bucket, bucketPolicyConfig); err == nil {
		var doc struct {
			Statement json.RawMessage `json:"Statement"`
		}

		if json.Unmarshal(raw, &doc) != nil {
			return nil
		}

		return decodeStatements(doc.Statement)
	}

	p, err := m.exportStore.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return nil
	}

	out := make([]policyStatement, 0, len(p.Statements))
	for _, s := range p.Statements {
		out = append(out, policyStatement{
			Effect: s.Effect, principals: []string{s.Principal}, actions: s.Actions, resources: s.Resources,
		})
	}

	return out
}

// decodeStatements accepts a single statement object or a list of them.
func decodeStatements(raw json.RawMessage) []policyStatement {
	var list []policyStatement
	if json.Unmarshal(raw, &list) != nil {
		var one policyStatement
		if json.Unmarshal(raw, &one) != nil {
			return nil
		}

		list = []policyStatement{one}
	}

	for i := range list {
		s := &list[i]
		s.principals = principalValues(s.Principal)
		s.actions = stringOrList(s.Action)
		s.resources = stringOrList(s.Resource)
	}

	return list
}

// principalValues flattens "*" or {"Service": ..., "AWS": ...} into values.
func principalValues(raw json.RawMessage) []string {
	if vals := stringOrList(raw); vals != nil {
		return vals
	}

	var byType map[string]json.RawMessage
	if json.Unmarshal(raw, &byType) != nil {
		return nil
	}

	var out []string
	for _, v := range byType {
		out = append(out, stringOrList(v)...)
	}

	return out
}

func stringOrList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}

	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}

	return nil
}

// logsAllowed reports whether the statements allow the CloudWatch Logs
// service principal action on resource, with an explicit Deny winning.
func logsAllowed(stmts []policyStatement, action, resource string) bool {
	allowed := false

	for i := range stmts {
		s := &stmts[i]
		if !anyMatch(s.principals, isLogsPrincipal) ||
			!anyMatch(s.actions, func(a string) bool { return wildcardMatch(a, action, true) }) ||
			!anyMatch(s.resources, func(r string) bool { return wildcardMatch(r, resource, false) }) {
			continue
		}

		if strings.EqualFold(s.Effect, "Deny") {
			return false
		}

		if strings.EqualFold(s.Effect, "Allow") {
			allowed = true
		}
	}

	return allowed
}

func anyMatch(vals []string, match func(string) bool) bool {
	for _, v := range vals {
		if match(v) {
			return true
		}
	}

	return false
}

// isLogsPrincipal matches "*", logs.amazonaws.com and logs.<region>.amazonaws.com.
// The region is not checked against the log group's region.
func isLogsPrincipal(p string) bool {
	if p == "*" || p == logsGlobalPrincipal {
		return true
	}

	rest, ok := strings.CutPrefix(p, logsServicePrefix)
	if !ok {
		return false
	}

	region, ok := strings.CutSuffix(rest, amazonawsSuffix)

	return ok && region != "" && !strings.ContainsAny(region, ".*")
}

// wildcardMatch matches an IAM pattern, where * is any run and ? any one
// character, across the whole value.
func wildcardMatch(pattern, value string, foldCase bool) bool {
	expr := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*"), `\?`, ".") + "$"
	if foldCase {
		expr = "(?i)" + expr
	}

	re, err := regexp.Compile(expr)

	return err == nil && re.MatchString(value)
}
