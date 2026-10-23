package cloudwatchlogs_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const liveTailSDKWait = 5 * time.Second

// liveTailEnv is a CloudWatch Logs server and a client whose transport dials
// it whatever the host, so the SDK's stream- host prefix stays in place.
type liveTailEnv struct {
	client    *cwl.Client
	transport *http.Transport
	clock     *config.FakeClock
	url       string
	cfg       aws.Config

	mu    sync.Mutex
	hosts []string
}

func newLiveTailEnv(t *testing.T) *liveTailEnv {
	t.Helper()

	env := &liveTailEnv{clock: config.NewFakeClock(time.Now())}
	cloud := cloudemu.NewAWS(config.WithClock(env.clock))
	srv := awsserver.New(awsserver.Drivers{CloudWatchLogs: cloud.CloudWatchLogs})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		env.hosts = append(env.hosts, r.Host)
		env.mu.Unlock()
		srv.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	env.url = ts.URL
	addr := ts.Listener.Addr().String()
	env.transport = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(env.transport.CloseIdleConnections)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithHTTPClient(&http.Client{Transport: env.transport}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	env.cfg = cfg
	env.client = cwl.NewFromConfig(cfg, func(o *cwl.Options) {
		o.BaseEndpoint = aws.String("http://logs.localhost.test")
	})

	return env
}

func (e *liveTailEnv) sawHost(host string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, h := range e.hosts {
		if h == host {
			return true
		}
	}

	return false
}

// groupARN creates a group and stream and returns the group's ARN.
func (e *liveTailEnv) groupARN(t *testing.T, group, stream string) string {
	t.Helper()

	mustLogGroupStream(t, e.client, group, stream)

	out, err := e.client.DescribeLogGroups(context.Background(), &cwl.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(group),
	})
	if err != nil || len(out.LogGroups) != 1 {
		t.Fatalf("DescribeLogGroups: %v %+v", err, out)
	}

	return aws.ToString(out.LogGroups[0].LogGroupArn)
}

func (e *liveTailEnv) put(t *testing.T, group, stream string, msgs ...string) {
	t.Helper()

	events := make([]cwltypes.InputLogEvent, 0, len(msgs))
	for _, m := range msgs {
		events = append(events, cwltypes.InputLogEvent{
			Timestamp: aws.Int64(e.clock.Now().UnixMilli()), Message: aws.String(m),
		})
	}

	mustSDK(t, "PutLogEvents", func() error {
		_, err := e.client.PutLogEvents(context.Background(), &cwl.PutLogEventsInput{
			LogGroupName: aws.String(group), LogStreamName: aws.String(stream), LogEvents: events,
		})

		return err
	})
}

// nextEvent reads the stream's next event, failing on timeout or stream end.
func nextEvent(t *testing.T, ev <-chan cwltypes.StartLiveTailResponseStream) cwltypes.StartLiveTailResponseStream {
	t.Helper()

	select {
	case e, ok := <-ev:
		if !ok {
			t.Fatal("Live Tail stream ended early")
		}

		return e
	case <-time.After(liveTailSDKWait):
		t.Fatal("no Live Tail event")
	}

	return nil
}

func TestSDKStartLiveTailStreamsMatchingEvents(t *testing.T) {
	env := newLiveTailEnv(t)
	arn := env.groupARN(t, "/lt/app", "web-1")
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := env.client.StartLiveTail(ctx, &cwl.StartLiveTailInput{
		LogGroupIdentifiers:   []string{arn},
		LogStreamNamePrefixes: []string{"web"},
		LogEventFilterPattern: aws.String("ERROR"),
	})
	if err != nil {
		t.Fatalf("StartLiveTail: %v", err)
	}

	stream := out.GetStream()
	ev := stream.Events()

	start, ok := nextEvent(t, ev).(*cwltypes.StartLiveTailResponseStreamMemberSessionStart)
	if !ok || aws.ToString(start.Value.SessionId) == "" || aws.ToString(start.Value.RequestId) == "" ||
		len(start.Value.LogGroupIdentifiers) != 1 || aws.ToString(start.Value.LogEventFilterPattern) != "ERROR" {
		t.Fatalf("sessionStart = %+v", start)
	}

	if !env.sawHost("stream-logs.localhost.test") {
		t.Fatal("StartLiveTail did not reach the stream-logs. host")
	}

	env.put(t, "/lt/app", "web-1", "ERROR boom", "INFO fine")

	for {
		up, ok := nextEvent(t, ev).(*cwltypes.StartLiveTailResponseStreamMemberSessionUpdate)
		if !ok {
			t.Fatal("expected a sessionUpdate")
		}

		if len(up.Value.SessionResults) == 0 {
			continue
		}

		r := up.Value.SessionResults
		if len(r) != 1 || aws.ToString(r[0].Message) != "ERROR boom" || aws.ToString(r[0].LogStreamName) != "web-1" ||
			aws.ToString(r[0].LogGroupIdentifier) != arn || aws.ToInt64(r[0].IngestionTime) == 0 ||
			up.Value.SessionMetadata == nil || up.Value.SessionMetadata.Sampled {
			t.Fatalf("sessionUpdate = %+v", up.Value)
		}

		break
	}

	// Ending the stream from the client ends the session on the server.
	_ = stream.Close()
	cancel()
	env.transport.CloseIdleConnections()

	deadline := time.Now().Add(liveTailSDKWait)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines after the session = %d, before = %d", n, before)
	}
}

func TestSDKStartLiveTailTimeout(t *testing.T) {
	env := newLiveTailEnv(t)
	arn := env.groupARN(t, "/lt/timeout", "s")

	out, err := env.client.StartLiveTail(context.Background(), &cwl.StartLiveTailInput{LogGroupIdentifiers: []string{arn}})
	if err != nil {
		t.Fatalf("StartLiveTail: %v", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	if _, ok := nextEvent(t, stream.Events()).(*cwltypes.StartLiveTailResponseStreamMemberSessionStart); !ok {
		t.Fatal("expected sessionStart first")
	}

	env.clock.Advance(3 * time.Hour)

	deadline := time.After(liveTailSDKWait)

	for {
		select {
		case _, ok := <-stream.Events():
			if ok {
				continue
			}

			var te *cwltypes.SessionTimeoutException
			if !errors.As(stream.Err(), &te) {
				t.Fatalf("stream error = %v, want SessionTimeoutException", stream.Err())
			}

			return
		case <-deadline:
			t.Fatal("session did not time out")
		}
	}
}

func TestSDKStartLiveTailErrors(t *testing.T) {
	env := newLiveTailEnv(t)
	arn := env.groupARN(t, "/lt/err", "s")
	missing := strings.Replace(arn, "/lt/err", "/lt/missing", 1)

	tests := []struct {
		name string
		in   *cwl.StartLiveTailInput
		want string
	}{
		{"name not ARN", &cwl.StartLiveTailInput{LogGroupIdentifiers: []string{"/lt/err"}}, "InvalidParameterException"},
		{"names and prefixes", &cwl.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogStreamNames: []string{"s"}, LogStreamNamePrefixes: []string{"s"},
		}, "InvalidParameterException"},
		{"bad pattern", &cwl.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogEventFilterPattern: aws.String("{ $.a = "),
		}, "InvalidParameterException"},
		{"unknown group", &cwl.StartLiveTailInput{LogGroupIdentifiers: []string{missing}}, "ResourceNotFoundException"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := env.client.StartLiveTail(context.Background(), tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %s", err, tt.want)
			}
		})
	}
}

// noHostPrefix is the documented way to reach a localhost endpoint from
// aws-sdk-go-v2: it turns off the stream- host prefix for every call.
func noHostPrefix(s *middleware.Stack) error {
	return s.Initialize.Add(middleware.InitializeMiddlewareFunc("noHostPrefix",
		func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
			middleware.InitializeOutput, middleware.Metadata, error,
		) {
			return next.HandleInitialize(smithyhttp.DisableEndpointHostPrefix(ctx, true), in)
		}), middleware.Before)
}

func TestSDKStartLiveTailWithoutHostPrefix(t *testing.T) {
	env := newLiveTailEnv(t)
	arn := env.groupARN(t, "/lt/plain", "s")

	// A plain 127.0.0.1 endpoint with the default transport, as a user would
	// configure it.
	cfg := env.cfg.Copy()
	cfg.HTTPClient = http.DefaultClient
	client := cwl.NewFromConfig(cfg, func(o *cwl.Options) {
		o.BaseEndpoint = aws.String(env.url)
		o.APIOptions = append(o.APIOptions, noHostPrefix)
	})

	out, err := client.StartLiveTail(context.Background(), &cwl.StartLiveTailInput{LogGroupIdentifiers: []string{arn}})
	if err != nil {
		t.Fatalf("StartLiveTail: %v", err)
	}

	stream := out.GetStream()
	defer stream.Close()

	if _, ok := nextEvent(t, stream.Events()).(*cwltypes.StartLiveTailResponseStreamMemberSessionStart); !ok {
		t.Fatal("expected sessionStart")
	}
}

// TestStartLiveTailStreamingHostPrefix sends the request the way botocore
// does, to the streaming- host prefix, and reads the frames.
func TestStartLiveTailStreamingHostPrefix(t *testing.T) {
	env := newLiveTailEnv(t)
	arn := env.groupARN(t, "/lt/botocore", "s")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := `{"logGroupIdentifiers":["` + arn + `"]}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://streaming-logs.localhost.test/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("X-Amz-Target", "Logs_20140328.StartLiveTail")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")

	resp, err := (&http.Client{Transport: env.transport}).Do(req)
	if err != nil {
		t.Fatalf("StartLiveTail: %v", err)
	}
	defer resp.Body.Close()

	if !env.sawHost("streaming-logs.localhost.test") {
		t.Fatal("StartLiveTail did not reach the streaming-logs. host")
	}

	dec := eventstream.NewDecoder()

	for _, want := range []string{"initial-response", "sessionStart", "sessionUpdate"} {
		msg, err := dec.Decode(resp.Body, nil)
		if err != nil {
			t.Fatalf("decode %s: %v", want, err)
		}

		if got := msg.Headers.Get(eventstreamapi.EventTypeHeader); got == nil || got.String() != want {
			t.Fatalf("event type = %v, want %s", got, want)
		}
	}
}
