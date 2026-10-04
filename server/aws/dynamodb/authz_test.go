package dynamodb_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server/aws/dynamodb"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const (
	tableT1 = "arn:aws:dynamodb:us-east-1:123456789012:table/t1"
	tableT2 = "arn:aws:dynamodb:us-east-1:123456789012:table/t2"
)

var ddbScope = awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"} //nolint:gochecknoglobals // test fixture

func ddbRequest(prefix, op, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("X-Amz-Target", prefix+op)
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")

	return r
}

type wantCheck struct{ action, resource string }

func checksOf(checks []awsauthz.Check) []wantCheck {
	out := make([]wantCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, wantCheck{c.Action, c.Resource})
	}

	return out
}

func one(action, resource string) []wantCheck { return []wantCheck{{action, resource}} }

func TestIAMChecksTableARNs(t *testing.T) {
	onT1 := `{"TableName":"t1"}`
	backup := tableT1 + "/backup/01700000000000-abcdef12"

	cases := []struct {
		op, body string
		want     []wantCheck
	}{
		{"CreateTable", onT1, one("dynamodb:CreateTable", tableT1)},
		{"DeleteTable", onT1, one("dynamodb:DeleteTable", tableT1)},
		{"DescribeTable", onT1, one("dynamodb:DescribeTable", tableT1)},
		{"UpdateTable", onT1, one("dynamodb:UpdateTable", tableT1)},
		{"DescribeContinuousBackups", onT1, one("dynamodb:DescribeContinuousBackups", tableT1)},
		{"UpdateContinuousBackups", onT1, one("dynamodb:UpdateContinuousBackups", tableT1)},
		{"ListTables", `{}`, one("dynamodb:ListTables", "*")},
		{"PutItem", onT1, one("dynamodb:PutItem", tableT1)},
		{"GetItem", onT1, one("dynamodb:GetItem", tableT1)},
		{"DeleteItem", onT1, one("dynamodb:DeleteItem", tableT1)},
		{"UpdateItem", onT1, one("dynamodb:UpdateItem", tableT1)},
		{"Query", onT1, one("dynamodb:Query", tableT1)},
		{"Query", `{"TableName":"t1","IndexName":"byOwner"}`, one("dynamodb:Query", tableT1+"/index/byOwner")},
		{"Scan", onT1, one("dynamodb:Scan", tableT1)},
		{"Scan", `{"TableName":"t1","IndexName":"byOwner"}`, one("dynamodb:Scan", tableT1+"/index/byOwner")},
		{"BatchWriteItem", `{"RequestItems":{"t2":[],"t1":[]}}`,
			[]wantCheck{{"dynamodb:BatchWriteItem", tableT1}, {"dynamodb:BatchWriteItem", tableT2}}},
		{"BatchGetItem", `{"RequestItems":{"t2":{},"t1":{}}}`,
			[]wantCheck{{"dynamodb:BatchGetItem", tableT1}, {"dynamodb:BatchGetItem", tableT2}}},
		{"TransactWriteItems", `{"TransactItems":[{"Put":{"TableName":"t1"}},{"Update":{"TableName":"t2"}},` +
			`{"Delete":{"TableName":"t1"}},{"ConditionCheck":{"TableName":"t2"}},{"Put":{"TableName":"t1"}}]}`,
			[]wantCheck{
				{"dynamodb:PutItem", tableT1}, {"dynamodb:UpdateItem", tableT2},
				{"dynamodb:DeleteItem", tableT1}, {"dynamodb:ConditionCheckItem", tableT2},
			}},
		// Dispatch runs the first kind set on an element (Put before Delete), so
		// that is the one authorized.
		{"TransactWriteItems", `{"TransactItems":[{"Delete":{"TableName":"t2"},"Put":{"TableName":"t1"}}]}`,
			one("dynamodb:PutItem", tableT1)},
		{"TransactGetItems", `{"TransactItems":[{"Get":{"TableName":"t2"}},{"Get":{"TableName":"t1"}},{"Get":{"TableName":"t2"}}]}`,
			[]wantCheck{{"dynamodb:GetItem", tableT2}, {"dynamodb:GetItem", tableT1}}},
		{"TagResource", `{"ResourceArn":"` + tableT1 + `"}`, one("dynamodb:TagResource", tableT1)},
		{"UntagResource", `{"ResourceArn":"` + tableT1 + `"}`, one("dynamodb:UntagResource", tableT1)},
		{"ListTagsOfResource", `{"ResourceArn":"` + tableT1 + `/index/i"}`, one("dynamodb:ListTagsOfResource", tableT1)},
		{"DescribeTimeToLive", onT1, one("dynamodb:DescribeTimeToLive", tableT1)},
		{"UpdateTimeToLive", onT1, one("dynamodb:UpdateTimeToLive", tableT1)},
		{"CreateBackup", onT1, one("dynamodb:CreateBackup", tableT1)},
		{"DescribeBackup", `{"BackupArn":"` + backup + `"}`, one("dynamodb:DescribeBackup", backup)},
		{"DeleteBackup", `{"BackupArn":"` + backup + `"}`, one("dynamodb:DeleteBackup", backup)},
		{"ListBackups", onT1, one("dynamodb:ListBackups", "*")},
		{"RestoreTableFromBackup", `{"BackupArn":"` + backup + `","TargetTableName":"t2"}`,
			[]wantCheck{{"dynamodb:RestoreTableFromBackup", backup}, {"dynamodb:RestoreTableFromBackup", tableT2}}},
		{"RestoreTableToPointInTime", `{"SourceTableName":"t1","TargetTableName":"t2"}`,
			[]wantCheck{{"dynamodb:RestoreTableToPointInTime", tableT1}, {"dynamodb:RestoreTableToPointInTime", tableT2}}},
		{"CreateGlobalTable", `{"GlobalTableName":"g"}`, one("dynamodb:CreateGlobalTable", "arn:aws:dynamodb::123456789012:global-table/g")},
		{"DescribeGlobalTable", `{"GlobalTableName":"g"}`, one("dynamodb:DescribeGlobalTable", "arn:aws:dynamodb::123456789012:global-table/g")},
		{"UpdateGlobalTable", `{"GlobalTableName":"g"}`, one("dynamodb:UpdateGlobalTable", "arn:aws:dynamodb::123456789012:global-table/g")},
		{"ListGlobalTables", `{}`, one("dynamodb:ListGlobalTables", "*")},
		{"DescribeKinesisStreamingDestination", onT1, one("dynamodb:DescribeKinesisStreamingDestination", tableT1)},
		{"EnableKinesisStreamingDestination", onT1, one("dynamodb:EnableKinesisStreamingDestination", tableT1)},
		{"DisableKinesisStreamingDestination", onT1, one("dynamodb:DisableKinesisStreamingDestination", tableT1)},
		{"UpdateKinesisStreamingDestination", onT1, one("dynamodb:UpdateKinesisStreamingDestination", tableT1)},
		{"DescribeContributorInsights", onT1, one("dynamodb:DescribeContributorInsights", tableT1)},
		{"UpdateContributorInsights", `{"TableName":"t1","IndexName":"i"}`, one("dynamodb:UpdateContributorInsights", tableT1+"/index/i")},
		{"ListContributorInsights", onT1, one("dynamodb:ListContributorInsights", "*")},
		{"DescribeLimits", `{}`, one("dynamodb:DescribeLimits", "*")},
		{"DescribeEndpoints", `{}`, one("dynamodb:DescribeEndpoints", "*")},
		// The account and region come from the server, not the ARN.
		{"TagResource", `{"ResourceArn":"arn:aws:dynamodb:eu-west-1:999999999999:table/t1"}`, one("dynamodb:TagResource", tableT1)},
		// Anything the request does not name cleanly stays unknown.
		{"PutItem", `{}`, one("dynamodb:PutItem", "")},
		{"PutItem", `{"TableName":"t*"}`, one("dynamodb:PutItem", "")},
		{"PutItem", `{"TableName":`, one("dynamodb:PutItem", "")},
		{"Query", `{"TableName":"t1","IndexName":"a/b"}`, one("dynamodb:Query", "")},
		{"BatchWriteItem", `{"RequestItems":{}}`, one("dynamodb:BatchWriteItem", "")},
		{"BatchWriteItem", `{"RequestItems":{"t1":[],"t*":[]}}`,
			[]wantCheck{{"dynamodb:BatchWriteItem", ""}, {"dynamodb:BatchWriteItem", tableT1}}},
		{"TransactWriteItems", `{"TransactItems":[]}`, one("dynamodb:PutItem", "")},
		{"TransactGetItems", `{"TransactItems":[{}]}`, one("dynamodb:GetItem", "")},
		{"DescribeBackup", `{"BackupArn":"nope"}`, one("dynamodb:DescribeBackup", "")},
		{"TagResource", `{"ResourceArn":"t1"}`, one("dynamodb:TagResource", tableT1)},
	}

	h := dynamodb.New(nil)

	for _, tc := range cases {
		checks, ok := h.IAMChecks(ddbRequest("DynamoDB_20120810.", tc.op, tc.body), ddbScope)
		if !ok || !reflect.DeepEqual(checksOf(checks), tc.want) {
			t.Errorf("%s %s: got %+v ok=%v, want %+v", tc.op, tc.body, checksOf(checks), ok, tc.want)
		}
	}

	for _, op := range []string{"", "Bogus", "putitem", "ExecuteStatement"} {
		if checks, ok := h.IAMChecks(ddbRequest("DynamoDB_20120810.", op, `{}`), ddbScope); ok {
			t.Errorf("%q: got %+v, want ok=false", op, checks)
		}
	}
}

func TestStreamsIAMChecks(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	if err := cloud.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{
		Name: "t1", PartitionKey: "id", StreamEnabled: true, StreamViewType: "NEW_IMAGE",
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	cfg, err := cloud.DynamoDB.DescribeTable(ctx, "t1")
	if err != nil || cfg.StreamArn == "" {
		t.Fatalf("DescribeTable: %+v %v", cfg, err)
	}

	label := cfg.StreamArn[strings.LastIndex(cfg.StreamArn, "/")+1:]
	stream := tableT1 + "/stream/" + label
	iterator := base64.URLEncoding.EncodeToString([]byte(`{"t":"t1","a":"","s":"shard"}`))
	noStream := base64.URLEncoding.EncodeToString([]byte(`{"t":"t2"}`))

	cases := []struct {
		op, body string
		want     []wantCheck
	}{
		{"ListStreams", onTable("t1"), one("dynamodb:ListStreams", "*")},
		{"DescribeStream", `{"StreamArn":"` + cfg.StreamArn + `"}`, one("dynamodb:DescribeStream", stream)},
		{"GetShardIterator", `{"StreamArn":"` + cfg.StreamArn + `"}`, one("dynamodb:GetShardIterator", stream)},
		// GetRecords runs on the table the iterator names, so its stream is
		// the resource.
		{"GetRecords", `{"ShardIterator":"` + iterator + `"}`, one("dynamodb:GetRecords", stream)},
		{"GetRecords", `{"ShardIterator":"` + noStream + `"}`, one("dynamodb:GetRecords", "")},
		{"GetRecords", `{"ShardIterator":"%%"}`, one("dynamodb:GetRecords", "")},
		{"DescribeStream", `{"StreamArn":"nope"}`, one("dynamodb:DescribeStream", "")},
	}

	h := dynamodb.NewStreams(cloud.DynamoDB)

	for _, tc := range cases {
		checks, ok := h.IAMChecks(ddbRequest("DynamoDBStreams_20120810.", tc.op, tc.body), ddbScope)
		if !ok || !reflect.DeepEqual(checksOf(checks), tc.want) {
			t.Errorf("%s %s: got %+v ok=%v, want %+v", tc.op, tc.body, checksOf(checks), ok, tc.want)
		}
	}

	if checks, ok := h.IAMChecks(ddbRequest("DynamoDBStreams_20120810.", "Bogus", `{}`), ddbScope); ok {
		t.Errorf("unknown streams op: got %+v, want ok=false", checks)
	}
}

func onTable(name string) string { return `{"TableName":"` + name + `"}` }
