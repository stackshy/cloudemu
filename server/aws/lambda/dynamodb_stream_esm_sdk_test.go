// dynamodb_stream_esm_sdk_test.go — real aws-sdk-go-v2 end-to-end test for the
// DynamoDB Streams -> Lambda event-source-mapping delivery path. Creating a
// mapping from a stream-enabled table to a function, then writing an item, must
// synchronously invoke the mapped function with a DynamoDB Streams event batch
// (previously CreateEventSourceMapping only stored config and nothing ever
// invoked the function).
package lambda_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func TestSDKDynamoDBStreamESMInvokesLambda(t *testing.T) {
	cloud := cloudemu.NewAWS()

	// Register an in-process handler for the target function so the test can
	// deterministically observe the invocation and inspect the delivered event.
	invocations := make(chan []byte, 4)
	cloud.Lambda.RegisterHandler("stream-processor", func(_ context.Context, payload []byte) ([]byte, error) {
		invocations <- payload
		return payload, nil
	})

	srv := awsserver.New(awsserver.Drivers{
		DynamoDB:   cloud.DynamoDB,
		Lambda:     cloud.Lambda,
		CloudWatch: cloud.CloudWatch,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ddb, lam := newDDBAndLambda(t, ts.URL)
	ctx := context.Background()

	streamARN := createStreamTable(t, ddb, "orders")
	createProcessorFunction(t, lam, "stream-processor")

	// Map the table's stream to the function, enabled.
	esm, err := lam.CreateEventSourceMapping(ctx, &awslambda.CreateEventSourceMappingInput{
		EventSourceArn:   aws.String(streamARN),
		FunctionName:     aws.String("stream-processor"),
		StartingPosition: lambdatypes.EventSourcePositionTrimHorizon,
		Enabled:          aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	if esm.State == nil || *esm.State != "Enabled" {
		t.Fatalf("mapping State = %v, want Enabled", esm.State)
	}

	// Writing an item to the streams-enabled table must invoke the mapped Lambda.
	if _, err := ddb.PutItem(ctx, &awsddb.PutItemInput{
		TableName: aws.String("orders"),
		Item: map[string]ddbtypes.AttributeValue{
			"Id":      &ddbtypes.AttributeValueMemberN{Value: "101"},
			"Message": &ddbtypes.AttributeValueMemberS{Value: "New item!"},
		},
	}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	payload := awaitInvocation(t, invocations)
	assertStreamEvent(t, payload, streamARN)
}

// assertStreamEvent verifies the payload Lambda received is the documented
// DynamoDB Streams event shape: a Records array carrying the eventSourceARN and
// an AttributeValue-encoded NewImage for the written item.
func assertStreamEvent(t *testing.T, payload []byte, streamARN string) {
	t.Helper()

	var event struct {
		Records []struct {
			EventName      string `json:"eventName"`
			EventSource    string `json:"eventSource"`
			EventSourceARN string `json:"eventSourceARN"`
			DynamoDB       struct {
				Keys     map[string]map[string]any `json:"Keys"`
				NewImage map[string]map[string]any `json:"NewImage"`
			} `json:"dynamodb"`
		} `json:"Records"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal stream event: %v\npayload=%s", err, payload)
	}

	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1\npayload=%s", len(event.Records), payload)
	}

	rec := event.Records[0]
	if rec.EventName != "INSERT" {
		t.Fatalf("eventName = %q, want INSERT", rec.EventName)
	}

	if rec.EventSource != "aws:dynamodb" {
		t.Fatalf("eventSource = %q, want aws:dynamodb", rec.EventSource)
	}

	if rec.EventSourceARN != streamARN {
		t.Fatalf("eventSourceARN = %q, want %q", rec.EventSourceARN, streamARN)
	}

	if got := rec.DynamoDB.NewImage["Message"]["S"]; got != "New item!" {
		t.Fatalf("NewImage.Message.S = %v, want %q\npayload=%s", got, "New item!", payload)
	}

	if got := rec.DynamoDB.Keys["Id"]["N"]; got != "101" {
		t.Fatalf("Keys.Id.N = %v, want 101\npayload=%s", got, payload)
	}
}

func awaitInvocation(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()

	select {
	case p := <-ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("mapped Lambda was never invoked by the DynamoDB stream write")
		return nil
	}
}

func createStreamTable(t *testing.T, ddb *awsddb.Client, name string) string {
	t.Helper()

	out, err := ddb.CreateTable(context.Background(), &awsddb.CreateTableInput{
		TableName: aws.String(name),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("Id"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("Id"), KeyType: ddbtypes.KeyTypeHash},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
		StreamSpecification: &ddbtypes.StreamSpecification{
			StreamEnabled:  aws.Bool(true),
			StreamViewType: ddbtypes.StreamViewTypeNewAndOldImages,
		},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	arn := aws.ToString(out.TableDescription.LatestStreamArn)
	if arn == "" {
		t.Fatal("CreateTable returned an empty LatestStreamArn")
	}

	return arn
}

func createProcessorFunction(t *testing.T, lam *awslambda.Client, name string) {
	t.Helper()

	if _, err := lam.CreateFunction(context.Background(), &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("stub")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
}

func newDDBAndLambda(t *testing.T, url string) (*awsddb.Client, *awslambda.Client) {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ddb := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(url) })
	lam := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(url) })

	return ddb, lam
}
