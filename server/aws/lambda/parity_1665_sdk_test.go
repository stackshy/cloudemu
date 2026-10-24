package lambda_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/stackshy/cloudemu/v2/config"
)

// echoCodeEngine is a FunctionEngine whose invocation returns the deployment
// package deployed under the invoked key, so a test sees which code ran.
type echoCodeEngine struct {
	mu       sync.Mutex
	deployed map[string][]byte
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (e *echoCodeEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.deployed[fn.Name] = fn.Code

	return nil
}

func (e *echoCodeEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	code, ok := e.deployed[name]
	if !ok {
		return config.FunctionResult{}, fmt.Errorf("%q not deployed", name)
	}

	return config.FunctionResult{Payload: code}, nil
}

func (e *echoCodeEngine) Remove(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.deployed, name)

	return nil
}

func invokePayload(t *testing.T, c *awslambda.Client, name string) string {
	t.Helper()

	out, err := c.Invoke(context.Background(), &awslambda.InvokeInput{FunctionName: aws.String(name)})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	return string(out.Payload)
}

// TestSDKInvokeRunsPreviousCodeWhileUpdateInProgress drives the real SDK: under
// AsyncSettle an Invoke right after UpdateFunctionCode still returns the old
// code's output while LastUpdateStatus is InProgress, and the new code's once
// the update is Successful. Without AsyncSettle the new code runs at once.
func TestSDKInvokeRunsPreviousCodeWhileUpdateInProgress(t *testing.T) {
	for _, async := range []bool{true, false} {
		t.Run(fmt.Sprintf("async=%v", async), func(t *testing.T) {
			fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
			opts := []config.Option{
				config.WithClock(fc),
				config.WithFunctionEngine(&echoCodeEngine{deployed: map[string][]byte{}}),
			}

			if async {
				opts = append(opts, config.WithAsyncSettle())
			}

			c := newParityLambdaClient(t, opts...)
			ctx := context.Background()

			createParityFunction(t, c, "swap", []byte(`"old"`))
			fc.Advance(2 * time.Second)

			upd, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
				FunctionName: aws.String("swap"), ZipFile: []byte(`"new"`),
			})
			if err != nil {
				t.Fatal(err)
			}

			want := `"new"`
			if async {
				want = `"old"`

				if upd.LastUpdateStatus != lambdatypes.LastUpdateStatusInProgress {
					t.Fatalf("LastUpdateStatus = %q, want InProgress", upd.LastUpdateStatus)
				}
			}

			if got := invokePayload(t, c, "swap"); got != want {
				t.Errorf("invoke right after the update = %s, want %s", got, want)
			}

			fc.Advance(2 * time.Second)

			if got := invokePayload(t, c, "swap"); got != `"new"` {
				t.Errorf("invoke after the update completed = %s, want \"new\"", got)
			}
		})
	}
}

// TestSDKUpdateFunctionCodePublishVersionIsPending: the version
// UpdateFunctionCode --publish cuts is Pending/Creating with no
// LastUpdateStatus while the update is InProgress (real Lambda's response and
// GetFunction --qualifier), and Active/Successful once it completes.
func TestSDKUpdateFunctionCodePublishVersionIsPending(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newParityLambdaClient(t, config.WithClock(fc), config.WithAsyncSettle())
	ctx := context.Background()

	createParityFunction(t, c, "pubupd", []byte("v1"))
	fc.Advance(2 * time.Second)

	upd, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("pubupd"), ZipFile: []byte("v2"), Publish: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	pending := versionSnapshot{
		lastModified: aws.ToString(upd.LastModified), state: string(lambdatypes.StatePending),
		stateReasonCode: string(lambdatypes.StateReasonCodeCreating),
	}

	got := snapshotFields(upd.LastModified, upd.State, upd.StateReasonCode, upd.LastUpdateStatus)
	if aws.ToString(upd.Version) != "1" || got != pending {
		t.Fatalf("UpdateFunctionCode --publish = %s %+v, want 1 %+v", aws.ToString(upd.Version), got, pending)
	}

	if aws.ToString(upd.StateReason) != "The function is being created." {
		t.Errorf("StateReason = %q, want The function is being created.", aws.ToString(upd.StateReason))
	}

	if upd.LastUpdateStatusReason != nil || upd.LastUpdateStatusReasonCode != "" {
		t.Errorf("version LastUpdateStatusReason = %q/%q, want none",
			aws.ToString(upd.LastUpdateStatusReason), upd.LastUpdateStatusReasonCode)
	}

	if got := getVersionSnapshot(t, c, "pubupd", "1"); got != pending {
		t.Fatalf("version 1 while the update is InProgress = %+v, want %+v", got, pending)
	}

	latest, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("pubupd")})
	if err != nil {
		t.Fatal(err)
	}

	if latest.State != lambdatypes.StateActive || latest.LastUpdateStatus != lambdatypes.LastUpdateStatusInProgress {
		t.Errorf("$LATEST = %q/%q, want Active/InProgress", latest.State, latest.LastUpdateStatus)
	}

	fc.Advance(2 * time.Second)

	active := versionSnapshot{
		lastModified: pending.lastModified, state: string(lambdatypes.StateActive),
		lastUpdateStatus: string(lambdatypes.LastUpdateStatusSuccessful),
	}
	if got := getVersionSnapshot(t, c, "pubupd", "1"); got != active {
		t.Errorf("version 1 once the update completed = %+v, want %+v", got, active)
	}
}

// TestSDKLatestInProgressReportsUpdateReason: while an update of $LATEST is
// InProgress, real Lambda reports LastUpdateStatusReason "The function is being
// created." and LastUpdateStatusReasonCode "Creating"; both are omitted once
// the update is Successful.
func TestSDKLatestInProgressReportsUpdateReason(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	c := newParityLambdaClient(t, config.WithClock(fc), config.WithAsyncSettle())
	ctx := context.Background()

	createParityFunction(t, c, "reason", []byte("v1"))
	fc.Advance(2 * time.Second)

	upd, err := c.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String("reason"), ZipFile: []byte("v2"),
	})
	if err != nil {
		t.Fatal(err)
	}

	get := func() *awslambda.GetFunctionConfigurationOutput {
		t.Helper()

		out, err := c.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{FunctionName: aws.String("reason")})
		if err != nil {
			t.Fatal(err)
		}

		return out
	}

	inProgress := get()

	for name, got := range map[string][2]string{
		"UpdateFunctionCode":       {aws.ToString(upd.LastUpdateStatusReason), string(upd.LastUpdateStatusReasonCode)},
		"GetFunctionConfiguration": {aws.ToString(inProgress.LastUpdateStatusReason), string(inProgress.LastUpdateStatusReasonCode)},
	} {
		if got != [2]string{"The function is being created.", "Creating"} {
			t.Errorf("%s LastUpdateStatusReason/Code while InProgress = %q, want The function is being created./Creating", name, got)
		}
	}

	fc.Advance(2 * time.Second)

	done := get()
	if done.LastUpdateStatus != lambdatypes.LastUpdateStatusSuccessful ||
		done.LastUpdateStatusReason != nil || done.LastUpdateStatusReasonCode != "" {
		t.Errorf("after the update = %q %q/%q, want Successful with no reason", done.LastUpdateStatus,
			aws.ToString(done.LastUpdateStatusReason), done.LastUpdateStatusReasonCode)
	}
}
