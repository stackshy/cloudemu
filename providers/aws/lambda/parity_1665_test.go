package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// codeEngine is a FunctionEngine whose invocation returns the code deployed
// under the invoked key, so a test can tell which revision served a call.
type codeEngine struct {
	mu       sync.Mutex
	deployed map[string]string
}

func newCodeEngine() *codeEngine {
	return &codeEngine{deployed: map[string]string{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (e *codeEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.deployed[fn.Name] = string(fn.Code)

	return nil
}

func (e *codeEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	code, ok := e.deployed[name]
	if !ok {
		return config.FunctionResult{}, fmt.Errorf("%q not deployed", name)
	}

	return config.FunctionResult{Payload: []byte(code)}, nil
}

func (e *codeEngine) Remove(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.deployed, name)

	return nil
}

func (e *codeEngine) keys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	keys := make([]string, 0, len(e.deployed))
	for k := range e.deployed {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

func newEngineSettleMock(eng config.FunctionEngine, settled bool) (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := []config.Option{config.WithClock(fc), config.WithRegion("us-east-1"), config.WithFunctionEngine(eng)}

	if settled {
		opts = append(opts, config.WithAsyncSettle())
	}

	return New(config.NewOptions(opts...)), fc
}

func invokedCode(t *testing.T, m *Mock, qualifier string) string {
	t.Helper()

	out, err := m.Invoke(context.Background(), driver.InvokeInput{FunctionName: "real-fn", Qualifier: qualifier})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	return string(out.Payload)
}

// TestInvokeRunsPreviousCodeWhileUpdateInProgress: under AsyncSettle, Invoke of
// $LATEST keeps running the code from before an UpdateFunctionCode until the
// update completes, as real Lambda documents; then it runs the new code.
func TestInvokeRunsPreviousCodeWhileUpdateInProgress(t *testing.T) {
	ctx := context.Background()
	eng := newCodeEngine()
	m, fc := newEngineSettleMock(eng, true)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v1")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	if got := invokedCode(t, m, ""); got != "v1" {
		t.Errorf("invoke during InProgress ran %q, want the previous code v1", got)
	}

	if got := invokedCode(t, m, latestVersion); got != "v1" {
		t.Errorf("invoke $LATEST during InProgress ran %q, want v1", got)
	}

	fc.Advance(2 * time.Second)

	if got := invokedCode(t, m, ""); got != "v2" {
		t.Errorf("invoke after the update completed ran %q, want v2", got)
	}

	// The next update retires the original deployment: only the live and the
	// prior revision's deployments remain.
	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v3")}); err != nil {
		t.Fatal(err)
	}

	if got := invokedCode(t, m, ""); got != "v2" {
		t.Errorf("invoke during the second update ran %q, want v2", got)
	}

	if keys := eng.keys(); len(keys) != 2 || slices.Contains(keys, "real-fn") {
		t.Errorf("engine deployments after the second update = %v, want the v2 and v3 keys only", keys)
	}

	fc.Advance(2 * time.Second)

	if got := invokedCode(t, m, ""); got != "v3" {
		t.Errorf("invoke after the second update ran %q, want v3", got)
	}

	if err := m.DeleteFunction(ctx, "real-fn"); err != nil {
		t.Fatal(err)
	}

	if keys := eng.keys(); len(keys) != 0 {
		t.Errorf("engine deployments after delete = %v, want none", keys)
	}
}

// TestInvokeRunsNewCodeImmediatelyByDefault: without AsyncSettle an update
// completes at once, so Invoke runs the new code right away, and the code is
// redeployed in place under the function name (the historical behavior).
func TestInvokeRunsNewCodeImmediatelyByDefault(t *testing.T) {
	ctx := context.Background()
	eng := newCodeEngine()
	m, _ := newEngineSettleMock(eng, false)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v1")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	if got := invokedCode(t, m, ""); got != "v2" {
		t.Errorf("invoke after update ran %q, want v2", got)
	}

	if keys := eng.keys(); !slices.Equal(keys, []string{"real-fn"}) {
		t.Errorf("engine deployments = %v, want [real-fn]", keys)
	}
}

// TestInvokeUsesPreviousConfigurationWhileUpdateInProgress: the previous
// configuration (including AWS-only settings such as the DeadLetterConfig an
// async failure routes to) is what an invocation of $LATEST sees during the
// window; a published version is immutable and is never swapped.
func TestInvokeUsesPreviousConfigurationWhileUpdateInProgress(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	if err := m.SetFunctionAWSConfig(ctx, "my-func", driver.AWSFunctionConfig{
		DeadLetterConfig: &driver.DeadLetterConfig{TargetArn: "arn:aws:sqs:us-east-1:123456789012:old"},
	}, true); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 42}); err != nil {
		t.Fatal(err)
	}

	if err := m.SetFunctionAWSConfig(ctx, "my-func", driver.AWSFunctionConfig{
		DeadLetterConfig: &driver.DeadLetterConfig{TargetArn: "arn:aws:sqs:us-east-1:123456789012:new"},
	}, false); err != nil {
		t.Fatal(err)
	}

	served := func(version string) funcData {
		fd, _ := m.funcs.Get("my-func")
		m.servePrior(&fd, version)

		return fd
	}

	during := served(latestVersion)
	if during.info.Timeout == 42 || !strings.HasSuffix(during.awsConfig.DeadLetterConfig.TargetArn, ":old") {
		t.Errorf("$LATEST during InProgress serves Timeout %d / DLQ %s, want the previous ones",
			during.info.Timeout, during.awsConfig.DeadLetterConfig.TargetArn)
	}

	if v := served("1"); v.info.Timeout != 42 {
		t.Errorf("a published version is swapped to the prior revision (Timeout %d)", v.info.Timeout)
	}

	fc.Advance(2 * time.Second)

	after := served(latestVersion)
	if after.info.Timeout != 42 || !strings.HasSuffix(after.awsConfig.DeadLetterConfig.TargetArn, ":new") {
		t.Errorf("$LATEST after the update serves Timeout %d / DLQ %s, want the new ones",
			after.info.Timeout, after.awsConfig.DeadLetterConfig.TargetArn)
	}
}

// TestEngineNameSurvivesSnapshot: the engine key a function's live code runs
// under is persisted, so a restored function still invokes its deployment;
// the prior revision is not (settle windows are not persisted).
func TestEngineNameSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	eng := newCodeEngine()
	m, fc := newEngineSettleMock(eng, true)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v1")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	if !json.Valid(snap) || !strings.Contains(string(snap), `"engineName":"real-fn.update-`) {
		t.Fatalf("snapshot does not carry the engine key: %s", snap)
	}

	restored, _ := newEngineSettleMock(eng, true)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	if got := invokedCode(t, restored, ""); got != "v2" {
		t.Errorf("restored function ran %q, want v2", got)
	}
}

// TestVersionPublishedWithUpdateIsPendingUntilUpdateCompletes: the version
// UpdateFunctionCode(Publish=true) cuts reports State Pending, reason
// Creating and no LastUpdateStatus while the update is InProgress, as real
// Lambda does; it is Active/Successful once the update completes and stays so
// across later $LATEST updates.
func TestVersionPublishedWithUpdateIsPendingUntilUpdateCompletes(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	v, err := m.PublishVersionOnWrite(ctx, "my-func", "")
	if err != nil {
		t.Fatal(err)
	}

	if v.State != statePending || v.StateReason != stateReasonCreating ||
		v.StateReasonCode != stateReasonCodeCreating || v.LastUpdateStatus != "" {
		t.Fatalf("version cut by the update = %q/%q/%q/%q, want Pending/%s/Creating/<empty>",
			v.State, v.StateReason, v.StateReasonCode, v.LastUpdateStatus, stateReasonCreating)
	}

	fc.Advance(2 * time.Second)

	assertVersionActive := func(when string) {
		t.Helper()

		vers, err := m.ListVersions(ctx, "my-func")
		if err != nil {
			t.Fatal(err)
		}

		for _, got := range vers {
			if got.Version == "1" && (got.State != stateActive || got.LastUpdateStatus != lastUpdateSuccessful) {
				t.Errorf("version 1 %s = %q/%q, want Active/Successful", when, got.State, got.LastUpdateStatus)
			}
		}
	}

	assertVersionActive("after the update completed")

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 9}); err != nil {
		t.Fatal(err)
	}

	assertVersionActive("during a later $LATEST update")
}

// TestRetiredDeploymentsAreRemovedAfterRestore: the prior revision is not
// persisted, so a snapshot records its deployment as retired; after a restore
// the next code update removes it, and DeleteFunction removes every deployment.
func TestRetiredDeploymentsAreRemovedAfterRestore(t *testing.T) {
	ctx := context.Background()
	eng := newCodeEngine()
	m, fc := newEngineSettleMock(eng, true)

	cfg := engineFuncConfig()
	cfg.Code = []byte("v1")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v2")}); err != nil {
		t.Fatal(err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored, rfc := newEngineSettleMock(eng, true)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	rfc.Advance(2 * time.Second)

	if _, err := restored.UpdateFunction(ctx, "real-fn", driver.FunctionConfig{Code: []byte("v3")}); err != nil {
		t.Fatal(err)
	}

	// v1's deployment ("real-fn") was the prior revision when the snapshot was
	// taken; only v2's (now the prior) and v3's remain.
	if keys := eng.keys(); len(keys) != 2 || slices.Contains(keys, "real-fn") {
		t.Errorf("engine deployments after the update = %v, want only v2's and v3's", keys)
	}

	if err := restored.DeleteFunction(ctx, "real-fn"); err != nil {
		t.Fatal(err)
	}

	if keys := eng.keys(); len(keys) != 0 {
		t.Errorf("engine deployments after delete = %v, want none", keys)
	}
}

// TestLatestInProgressReportsUpdateReason: while an update is InProgress,
// $LATEST reports LastUpdateStatusReason "The function is being created." and
// LastUpdateStatusReasonCode "Creating", as real Lambda does; both clear once
// the update is Successful.
func TestLatestInProgressReportsUpdateReason(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	created, err := m.GetFunction(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	if created.LastUpdateStatusReason != "" || created.LastUpdateStatusReasonCode != "" {
		t.Errorf("Pending function reports update reason %q/%q, want none",
			created.LastUpdateStatusReason, created.LastUpdateStatusReasonCode)
	}

	fc.Advance(2 * time.Second)

	upd, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Description: "d2"})
	if err != nil {
		t.Fatal(err)
	}

	if upd.LastUpdateStatusReason != stateReasonCreating || upd.LastUpdateStatusReasonCode != stateReasonCodeCreating {
		t.Errorf("InProgress update reason = %q/%q, want %q/%q", upd.LastUpdateStatusReason,
			upd.LastUpdateStatusReasonCode, stateReasonCreating, stateReasonCodeCreating)
	}

	fc.Advance(2 * time.Second)

	done, err := m.GetFunction(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	if done.LastUpdateStatus != lastUpdateSuccessful || done.LastUpdateStatusReason != "" ||
		done.LastUpdateStatusReasonCode != "" {
		t.Errorf("after the update = %q %q/%q, want Successful with no reason", done.LastUpdateStatus,
			done.LastUpdateStatusReason, done.LastUpdateStatusReasonCode)
	}
}
