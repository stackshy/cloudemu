package eventbridge_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// TestSDKAPIDestinationTargetDelivers puts an event through the SDK and checks
// the API destination's endpoint receives it with the API key header and the
// target's path and query parameters, and that HttpParameters round-trips on
// ListTargetsByRule.
func TestSDKAPIDestinationTargetDelivers(t *testing.T) {
	var (
		mu   sync.Mutex
		got  *http.Request
		body string
	)

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()
		got, body = r.Clone(context.Background()), string(b)
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	eb, _ := newEBAndSecrets(t)
	ctx := context.Background()

	conn, err := eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name: aws.String("c1"), AuthorizationType: ebtypes.ConnectionAuthorizationTypeApiKey,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			ApiKeyAuthParameters: &ebtypes.CreateConnectionApiKeyAuthRequestParameters{
				ApiKeyName: aws.String("x-api-key"), ApiKeyValue: aws.String("s3cr3t"),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("d1"), ConnectionArn: conn.ConnectionArn,
		InvocationEndpoint: aws.String(hook.URL + "/orders/*"), HttpMethod: ebtypes.ApiDestinationHttpMethodPost,
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}

	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String("r"), EventPattern: aws.String(`{"source":["app"]}`)}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	params := &ebtypes.HttpParameters{
		PathParameterValues:   []string{"o-1"},
		QueryStringParameters: map[string]string{"q": "1"},
	}

	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{
		Rule:    aws.String("r"),
		Targets: []ebtypes.Target{{Id: aws.String("d"), Arn: dest.ApiDestinationArn, HttpParameters: params}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	list, err := eb.ListTargetsByRule(ctx, &awseb.ListTargetsByRuleInput{Rule: aws.String("r")})
	if err != nil || len(list.Targets) != 1 || list.Targets[0].HttpParameters == nil ||
		list.Targets[0].HttpParameters.PathParameterValues[0] != "o-1" {
		t.Fatalf("ListTargetsByRule = %+v, %v", list, err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String(`{"id":"42"}`)},
	}}); err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if got == nil {
		t.Fatal("the API destination endpoint was not called")
	}

	if got.URL.Path != "/orders/o-1" || got.URL.Query().Get("q") != "1" || got.Header.Get("x-api-key") != "s3cr3t" {
		t.Fatalf("request = %s %s headers %v", got.Method, got.URL, got.Header)
	}

	if body == "" {
		t.Fatal("the endpoint got an empty body")
	}
}
