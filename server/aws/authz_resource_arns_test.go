package aws

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

const (
	arnNS     = "arn:aws:iam::123456789012:"
	ddbNS     = "arn:aws:dynamodb:us-east-1:123456789012:table/"
	amzJSON10 = "application/x-amz-json-1.0"
	t1ARN     = "arn:aws:sns:us-east-1:123456789012:t1"
	t2ARN     = "arn:aws:sns:us-east-1:123456789012:t2"
)

func scoped(effect, action, resource string) string {
	return `{"Effect":"` + effect + `","Action":"` + action + `","Resource":"` + resource + `"}`
}

func policyDoc(statements ...string) string {
	doc := `{"Version":"2012-10-17","Statement":[`

	for i, s := range statements {
		if i > 0 {
			doc += ","
		}

		doc += s
	}

	return doc + `]}`
}

func rpcReq(service, target, body string) sreq {
	return sreq{path: "/", ctype: amzJSON10, body: body, service: service, header: map[string]string{"X-Amz-Target": target}}
}

func queueMessages(t *testing.T, cloud *awsprovider.Provider, url string) int {
	t.Helper()

	info, err := cloud.SQS.GetQueueInfo(context.Background(), url)
	if err != nil {
		t.Fatalf("GetQueueInfo: %v", err)
	}

	return info.ApproxMessageCount
}

// TestAuthzResourceScopedSQS: an Allow scoped to one queue covers that queue
// only, for single and batch operations alike.
func TestAuthzResourceScopedSQS(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	q1, err := cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "q1"})
	if err != nil {
		t.Fatalf("CreateQueue q1: %v", err)
	}

	q2, err := cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "q2"})
	if err != nil {
		t.Fatalf("CreateQueue q2: %v", err)
	}

	sender := userWithPolicy(t, cloud, "q1sender", policyDoc(scoped("Allow", "sqs:SendMessage", q1.ARN)))

	send := func(url string) sreq {
		return rpcReq("sqs", "AmazonSQS.SendMessage", `{"QueueUrl":"`+url+`","MessageBody":"hi"}`)
	}
	batch := func(url string) sreq {
		return rpcReq("sqs", "AmazonSQS.SendMessageBatch",
			`{"QueueUrl":"`+url+`","Entries":[{"Id":"a","MessageBody":"x"}]}`)
	}

	status, body := doSigned(t, ts, sender, send(q1.URL))
	if status != http.StatusOK {
		t.Fatalf("SendMessage q1: %d %s", status, body)
	}

	status, body = doSigned(t, ts, sender, batch(q1.URL))
	if status != http.StatusOK {
		t.Fatalf("SendMessageBatch q1 (authorized as sqs:SendMessage): %d %s", status, body)
	}

	status, body = doSigned(t, ts, sender, send(q2.URL))
	wantDenied(t, status, body, "sqs:SendMessage on resource: "+q2.ARN)

	status, body = doSigned(t, ts, sender, batch(q2.URL))
	wantDenied(t, status, body, "sqs:SendMessage on resource: "+q2.ARN)

	status, body = doSigned(t, ts, sender, rpcReq("sqs", "AmazonSQS.PurgeQueue", `{"QueueUrl":"`+q1.URL+`"}`))
	wantDenied(t, status, body, "sqs:PurgeQueue")

	if n := queueMessages(t, cloud, q1.URL); n != 2 {
		t.Errorf("q1 holds %d messages, want 2", n)
	}

	if n := queueMessages(t, cloud, q2.URL); n != 0 {
		t.Errorf("q2 holds %d messages, want 0", n)
	}
}

// TestAuthzResourceScopedSNS: Publish and Subscribe are authorized on the
// topic, and subscription operations on the subscription's topic.
func TestAuthzResourceScopedSNS(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	_, err := cloud.SNS.CreateTopic(ctx, notifdriver.TopicConfig{Name: "t1"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	_, err = cloud.SNS.CreateTopic(ctx, notifdriver.TopicConfig{Name: "t2"})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	pub := userWithPolicy(t, cloud, "t1pub", policyDoc(scoped("Allow", "sns:*", t1ARN)))

	publish := func(param, arn string) sreq {
		return form("sns", url.Values{"Action": {"Publish"}, param: {arn}, "Message": {"m"}}.Encode())
	}

	for _, param := range []string{"TopicArn", "TargetArn"} {
		status, body := doSigned(t, ts, pub, publish(param, t1ARN))
		if status != http.StatusOK {
			t.Fatalf("Publish %s t1: %d %s", param, status, body)
		}

		status, body = doSigned(t, ts, pub, publish(param, t2ARN))
		wantDenied(t, status, body, "sns:Publish on resource: "+t2ARN)
	}

	status, body := doSigned(t, ts, pub, form("sns", url.Values{
		"Action": {"Subscribe"}, "TopicArn": {t2ARN}, "Protocol": {"sqs"}, "Endpoint": {"arn:aws:sqs:us-east-1:123456789012:q"},
	}.Encode()))
	wantDenied(t, status, body, "sns:Subscribe")

	sub, err := cloud.SNS.Subscribe(ctx, notifdriver.SubscriptionConfig{TopicID: "t2", Protocol: "sqs", Endpoint: "arn:aws:sqs:us-east-1:123456789012:q"})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	status, body = doSigned(t, ts, pub, form("sns", url.Values{"Action": {"Unsubscribe"}, "SubscriptionArn": {sub.ID}}.Encode()))
	wantDenied(t, status, body, "sns:Unsubscribe on resource: "+t2ARN)

	status, body = doSigned(t, ts, pub, form("sns", url.Values{"Action": {"ListTopics"}}.Encode()))
	if status != http.StatusForbidden {
		t.Fatalf("ListTopics on * with a topic-scoped Allow: %d %s", status, body)
	}
}

// TestAuthzResourceScopedDynamoDB: a Deny on one table blocks that table
// only, including through the batch and transaction operations, and an index
// is its own resource.
func TestAuthzResourceScopedDynamoDB(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	for _, name := range []string{"dev", "prod"} {
		if err := cloud.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{
			Name: name, PartitionKey: "id",
			GSIs: []dbdriver.GSIConfig{{Name: "byOwner", PartitionKey: "owner"}},
		}); err != nil {
			t.Fatalf("CreateTable %s: %v", name, err)
		}
	}

	allButProd := userWithPolicy(t, cloud, "notprod", policyDoc(
		scoped("Allow", "dynamodb:*", "*"),
		scoped("Deny", "dynamodb:*", ddbNS+"prod"),
	))

	put := func(table string) sreq {
		return rpcReq("dynamodb", "DynamoDB_20120810.PutItem", `{"TableName":"`+table+`","Item":{"id":{"S":"1"}}}`)
	}
	batchWrite := func(tables ...string) sreq {
		items := ""
		for i, tb := range tables {
			if i > 0 {
				items += ","
			}

			items += `"` + tb + `":[{"PutRequest":{"Item":{"id":{"S":"b"}}}}]`
		}

		return rpcReq("dynamodb", "DynamoDB_20120810.BatchWriteItem", `{"RequestItems":{`+items+`}}`)
	}
	transact := func(tables ...string) sreq {
		items := ""
		for i, tb := range tables {
			if i > 0 {
				items += ","
			}

			items += `{"Put":{"TableName":"` + tb + `","Item":{"id":{"S":"t` + tb + `"}}}}`
		}

		return rpcReq("dynamodb", "DynamoDB_20120810.TransactWriteItems", `{"TransactItems":[`+items+`]}`)
	}

	for name, rq := range map[string]sreq{
		"PutItem dev":            put("dev"),
		"BatchWriteItem dev":     batchWrite("dev"),
		"TransactWriteItems dev": transact("dev"),
	} {
		status, body := doSigned(t, ts, allButProd, rq)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
	}

	for name, rq := range map[string]sreq{
		"PutItem prod":                put("prod"),
		"BatchWriteItem dev+prod":     batchWrite("dev", "prod"),
		"TransactWriteItems dev+prod": transact("dev", "prod"),
	} {
		t.Run(name, func(t *testing.T) {
			status, body := doSigned(t, ts, allButProd, rq)
			wantDenied(t, status, body, "on resource: "+ddbNS+"prod with an explicit deny")
		})
	}

	if items, _ := cloud.DynamoDB.Scan(ctx, dbdriver.ScanInput{Table: "prod"}); items != nil && len(items.Items) != 0 {
		t.Errorf("prod holds %d items after denied writes", len(items.Items))
	}

	indexOnly := userWithPolicy(t, cloud, "indexreader", policyDoc(scoped("Allow", "dynamodb:Query", ddbNS+"dev/index/byOwner")))
	query := func(index string) sreq {
		body := `{"TableName":"dev","KeyConditionExpression":"#k = :v",` +
			`"ExpressionAttributeNames":{"#k":"owner"},"ExpressionAttributeValues":{":v":{"S":"x"}}`
		if index != "" {
			body += `,"IndexName":"` + index + `"`
		}

		return rpcReq("dynamodb", "DynamoDB_20120810.Query", body+`}`)
	}

	status, body := doSigned(t, ts, indexOnly, query("byOwner"))
	if status != http.StatusOK {
		t.Fatalf("Query on the allowed index: %d %s", status, body)
	}

	status, body = doSigned(t, ts, indexOnly, query(""))
	wantDenied(t, status, body, "dynamodb:Query on resource: "+ddbNS+"dev ")
}

// TestAuthzResourceScopedIAM: an Allow on a user path covers the users under
// that path, found by name through their stored ARN.
func TestAuthzResourceScopedIAM(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "alice", Path: "/dev/"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "bob"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	devAdmin := userWithPolicy(t, cloud, "devadmin", policyDoc(scoped("Allow", "iam:*", arnNS+"user/dev/*")))

	iamForm := func(kv ...string) sreq {
		v := url.Values{"Version": {"2010-05-08"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}

		return form("iam", v.Encode())
	}

	for name, rq := range map[string]sreq{
		"GetUser alice":         iamForm("Action", "GetUser", "UserName", "alice"),
		"TagUser alice":         iamForm("Action", "TagUser", "UserName", "alice", "Tags.member.1.Key", "k", "Tags.member.1.Value", "v"),
		"CreateUser /dev/carol": iamForm("Action", "CreateUser", "UserName", "carol", "Path", "/dev/"),
	} {
		status, body := doSigned(t, ts, devAdmin, rq)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
	}

	for name, rq := range map[string]sreq{
		"GetUser bob":       iamForm("Action", "GetUser", "UserName", "bob"),
		"DeleteUser bob":    iamForm("Action", "DeleteUser", "UserName", "bob"),
		"CreateUser /dave":  iamForm("Action", "CreateUser", "UserName", "dave"),
		"CreateRole /dev/r": iamForm("Action", "CreateRole", "RoleName", "r", "Path", "/dev/", "AssumeRolePolicyDocument", "{}"),
		"ListUsers":         iamForm("Action", "ListUsers"),
	} {
		t.Run(name, func(t *testing.T) {
			status, body := doSigned(t, ts, devAdmin, rq)
			wantDenied(t, status, body, xmlAccessDenied)
		})
	}

	if userCount(t, cloud, "dave") != 0 || userCount(t, cloud, "bob") != 1 || userCount(t, cloud, "carol") != 1 {
		t.Error("IAM state does not match the authorized requests")
	}
}

// TestAuthzMessageMoveTaskDestination: a redrive needs sqs:SendMessage on its
// destination, so a caller denied the destination cannot move messages into
// it, and an empty DestinationArn (back to the original queues) is evaluated
// on an unknown resource.
func TestAuthzMessageMoveTaskDestination(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	ctx := context.Background()

	dlq, err := cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "dlq"})
	if err != nil {
		t.Fatalf("CreateQueue dlq: %v", err)
	}

	dest, err := cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "dest"})
	if err != nil {
		t.Fatalf("CreateQueue dest: %v", err)
	}

	if _, err := cloud.SQS.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: dlq.URL, Body: "m"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	sourceOnly := userWithPolicy(t, cloud, "redriver", policyDoc(scoped("Allow", "sqs:*", dlq.ARN)))
	start := func(destination string) sreq {
		body := `{"SourceArn":"` + dlq.ARN + `"`
		if destination != "" {
			body += `,"DestinationArn":"` + destination + `"`
		}

		return rpcReq("sqs", "AmazonSQS.StartMessageMoveTask", body+`}`)
	}

	status, body := doSigned(t, ts, sourceOnly, start(dest.ARN))
	wantDenied(t, status, body, "sqs:SendMessage on resource: "+dest.ARN)

	status, body = doSigned(t, ts, sourceOnly, start(""))
	wantDenied(t, status, body, "sqs:SendMessage on resource: *")

	if n := queueMessages(t, cloud, dest.URL); n != 0 {
		t.Fatalf("dest holds %d messages after denied redrives", n)
	}

	if n := queueMessages(t, cloud, dlq.URL); n != 1 {
		t.Fatalf("dlq holds %d messages after denied redrives, want 1", n)
	}

	both := userWithPolicy(t, cloud, "redriver2", policyDoc(
		scoped("Allow", "sqs:*", dlq.ARN), scoped("Allow", "sqs:SendMessage", dest.ARN)))

	status, body = doSigned(t, ts, both, start(dest.ARN))
	if status != http.StatusOK {
		t.Fatalf("redrive with SendMessage on the destination: %d %s", status, body)
	}

	if n := queueMessages(t, cloud, dest.URL); n != 1 {
		t.Fatalf("dest holds %d messages, want 1", n)
	}
}

// TestAuthzUnservedJSONRPCOperationIsNamed: an operation a JSON-RPC handler
// does not serve is denied to a restricted caller under its real name.
func TestAuthzUnservedJSONRPCOperationIsNamed(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	reader := userWithPolicy(t, cloud, "ddbreader", policyDoc(scoped("Allow", "dynamodb:GetItem", "*")))

	status, body := doSigned(t, ts, reader, rpcReq("dynamodb", "DynamoDB_20120810.ExecuteStatement", `{}`))
	wantDenied(t, status, body, "dynamodb:ExecuteStatement")
}
