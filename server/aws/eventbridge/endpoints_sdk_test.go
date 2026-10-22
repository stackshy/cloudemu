package eventbridge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	sdkHealthCheck = "arn:aws:route53:::healthcheck/hc-1"
	sdkWestBusARN  = "arn:aws:events:us-west-2:123456789012:event-bus/"
)

// authRecorder keeps the Authorization header of every PutEvents request.
type authRecorder struct {
	mu    sync.Mutex
	auths []string
	next  http.Handler
}

func (a *authRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Amz-Target") == "AWSEvents.PutEvents" {
		a.mu.Lock()
		a.auths = append(a.auths, r.Header.Get("Authorization"))
		a.mu.Unlock()
	}

	a.next.ServeHTTP(w, r)
}

func newEndpointClient(t *testing.T) (*awseb.Client, *authRecorder) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	rec := &authRecorder{next: awsserver.New(awsserver.Drivers{EventBridge: cloud.EventBridge})}

	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) }), rec
}

func endpointInput(name string) *awseb.CreateEndpointInput {
	return &awseb.CreateEndpointInput{
		Name: aws.String(name),
		RoutingConfig: &ebtypes.RoutingConfig{FailoverConfig: &ebtypes.FailoverConfig{
			Primary:   &ebtypes.Primary{HealthCheck: aws.String(sdkHealthCheck)},
			Secondary: &ebtypes.Secondary{Route: aws.String("us-west-2")},
		}},
		ReplicationConfig: &ebtypes.ReplicationConfig{State: ebtypes.ReplicationStateDisabled},
		EventBuses: []ebtypes.EndpointEventBus{
			{EventBusArn: aws.String(sdkBusARN + "b1")}, {EventBusArn: aws.String(sdkWestBusARN + "b1")},
		},
	}
}

// TestSDKEndpointLifecycle drives global endpoint CRUD through the real SDK.
func TestSDKEndpointLifecycle(t *testing.T) {
	eb, _ := newEndpointClient(t)
	ctx := context.Background()

	created, err := eb.CreateEndpoint(ctx, endpointInput("ep1"))
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	if created.State != ebtypes.EndpointStateCreating || aws.ToString(created.Arn) == "" ||
		len(created.EventBuses) != 2 || created.ReplicationConfig.State != ebtypes.ReplicationStateDisabled {
		t.Fatalf("CreateEndpoint = %+v", created)
	}

	desc, err := eb.DescribeEndpoint(ctx, &awseb.DescribeEndpointInput{Name: aws.String("ep1")})
	if err != nil {
		t.Fatalf("DescribeEndpoint: %v", err)
	}

	id := aws.ToString(desc.EndpointId)
	if desc.State != ebtypes.EndpointStateActive || aws.ToString(desc.EndpointUrl) != "https://"+id+".endpoint.events.amazonaws.com" ||
		aws.ToString(desc.RoutingConfig.FailoverConfig.Primary.HealthCheck) != sdkHealthCheck || desc.CreationTime == nil {
		t.Fatalf("DescribeEndpoint = %+v", desc)
	}

	upd, err := eb.UpdateEndpoint(ctx, &awseb.UpdateEndpointInput{
		Name: aws.String("ep1"), Description: aws.String("d"),
		ReplicationConfig: &ebtypes.ReplicationConfig{State: ebtypes.ReplicationStateEnabled},
		RoleArn:           aws.String("arn:aws:iam::123456789012:role/r"),
	})
	if err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}

	if upd.State != ebtypes.EndpointStateUpdating || aws.ToString(upd.EndpointId) != id {
		t.Fatalf("UpdateEndpoint = %+v", upd)
	}

	list, err := eb.ListEndpoints(ctx, &awseb.ListEndpointsInput{NamePrefix: aws.String("ep"), HomeRegion: aws.String("us-east-1")})
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}

	if len(list.Endpoints) != 1 || aws.ToString(list.Endpoints[0].Description) != "d" ||
		list.Endpoints[0].State != ebtypes.EndpointStateActive {
		t.Fatalf("ListEndpoints = %+v", list.Endpoints)
	}

	if _, err := eb.DeleteEndpoint(ctx, &awseb.DeleteEndpointInput{Name: aws.String("ep1")}); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}

	_, err = eb.DescribeEndpoint(ctx, &awseb.DescribeEndpointInput{Name: aws.String("ep1")})
	requireAPIError(t, err, "ResourceNotFoundException", "Endpoint ep1 does not exist.")
}

func TestSDKEndpointErrors(t *testing.T) {
	eb, _ := newEndpointClient(t)
	ctx := context.Background()

	if _, err := eb.CreateEndpoint(ctx, endpointInput("ep")); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	_, err := eb.CreateEndpoint(ctx, endpointInput("ep"))
	requireAPIError(t, err, "ResourceAlreadyExistsException", "Endpoint ep already exists.")

	in := endpointInput("ep2")
	in.ReplicationConfig = nil
	_, err = eb.CreateEndpoint(ctx, in)
	requireAPIError(t, err, "ValidationException", "")

	in = endpointInput("ep3")
	in.EventBuses[1].EventBusArn = aws.String(sdkWestBusARN + "other")
	_, err = eb.CreateEndpoint(ctx, in)
	requireAPIError(t, err, "ValidationException", "")

	_, err = eb.UpdateEndpoint(ctx, &awseb.UpdateEndpointInput{Name: aws.String("missing")})
	requireAPIError(t, err, "ResourceNotFoundException", "")

	_, err = eb.DeleteEndpoint(ctx, &awseb.DeleteEndpointInput{Name: aws.String("missing")})
	requireAPIError(t, err, "ResourceNotFoundException", "")
}

func TestSDKListEndpointsPaging(t *testing.T) {
	eb, _ := newEndpointClient(t)
	ctx := context.Background()

	for _, n := range []string{"e1", "e2", "e3"} {
		if _, err := eb.CreateEndpoint(ctx, endpointInput(n)); err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
	}

	var names []string

	in := &awseb.ListEndpointsInput{MaxResults: aws.Int32(2)}

	for pages := 0; ; pages++ {
		page, err := eb.ListEndpoints(ctx, in)
		if err != nil {
			t.Fatalf("ListEndpoints: %v", err)
		}

		for _, e := range page.Endpoints {
			names = append(names, aws.ToString(e.Name))
		}

		if page.NextToken == nil || pages > 3 {
			break
		}

		in.NextToken = page.NextToken
	}

	if strings.Join(names, ",") != "e1,e2,e3" {
		t.Fatalf("paged names = %v", names)
	}
}

// TestSDKPutEventsWithEndpointID proves the SDK still reaches a custom
// BaseEndpoint when EndpointId is set (it signs with SigV4a), and that the
// events land on the local primary bus.
func TestSDKPutEventsWithEndpointID(t *testing.T) {
	eb, rec := newEndpointClient(t)
	ctx := context.Background()

	if _, err := eb.CreateEventBus(ctx, &awseb.CreateEventBusInput{Name: aws.String("b1")}); err != nil {
		t.Fatalf("CreateEventBus: %v", err)
	}

	if _, err := eb.CreateEndpoint(ctx, endpointInput("ep")); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	desc, err := eb.DescribeEndpoint(ctx, &awseb.DescribeEndpointInput{Name: aws.String("ep")})
	if err != nil {
		t.Fatalf("DescribeEndpoint: %v", err)
	}

	out, err := eb.PutEvents(ctx, &awseb.PutEventsInput{
		EndpointId: desc.EndpointId,
		Entries: []ebtypes.PutEventsRequestEntry{{
			Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String("{}"),
			EventBusName: aws.String(sdkWestBusARN + "b1"),
		}},
	})
	if err != nil {
		t.Fatalf("PutEvents: %v", err)
	}

	if out.FailedEntryCount != 0 || aws.ToString(out.Entries[0].EventId) == "" {
		t.Fatalf("PutEvents = %+v", out.Entries)
	}

	if len(rec.auths) != 1 || !strings.HasPrefix(rec.auths[0], "AWS4-ECDSA-P256-SHA256") {
		t.Fatalf("PutEvents Authorization = %v, want a SigV4a signature", rec.auths)
	}

	_, err = eb.PutEvents(ctx, &awseb.PutEventsInput{
		EndpointId: aws.String("missing.abc"),
		Entries:    []ebtypes.PutEventsRequestEntry{{Source: aws.String("app"), DetailType: aws.String("t"), Detail: aws.String("{}")}},
	})
	requireAPIError(t, err, "ValidationException", "Parameter EndpointId is not valid. Reason: Endpoint missing.abc does not exist.")
}
