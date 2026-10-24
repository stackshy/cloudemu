package lambda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestPublishedVersionKeepsItsCodeSizeAndConfig: a published version is
// immutable, so UpdateFunctionCode/Configuration on $LATEST must not change the
// version's CodeSize, Environment or AWS-only settings.
func TestPublishedVersionKeepsItsCodeSizeAndConfig(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg := defaultFuncConfig()
	cfg.Code = []byte("small")
	cfg.Environment = map[string]string{"K": "v1"}

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := m.SetFunctionAWSConfig(ctx, "my-func",
		driver.AWSFunctionConfig{Architectures: []string{"x86_64"}}, true); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{
		Code: []byte("a much larger deployment package"), Environment: map[string]string{"K": "v2"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.SetFunctionAWSConfig(ctx, "my-func",
		driver.AWSFunctionConfig{Architectures: []string{"arm64"}}, false); err != nil {
		t.Fatal(err)
	}

	vers, err := m.ListVersions(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	var v1, latest driver.FunctionVersion

	for _, v := range vers {
		switch v.Version {
		case "1":
			v1 = v
		case latestVersion:
			latest = v
		}
	}

	if v1.CodeSize != int64(len("small")) {
		t.Errorf("version 1 CodeSize = %d, want %d", v1.CodeSize, len("small"))
	}

	if latest.CodeSize != int64(len("a much larger deployment package")) {
		t.Errorf("$LATEST CodeSize = %d", latest.CodeSize)
	}

	if v1.Environment["K"] != "v1" {
		t.Errorf("version 1 Environment = %v, want K=v1", v1.Environment)
	}

	awsCfg, err := m.GetVersionAWSConfig(ctx, "my-func", "1")
	if err != nil {
		t.Fatal(err)
	}

	if len(awsCfg.Architectures) != 1 || awsCfg.Architectures[0] != "x86_64" {
		t.Errorf("version 1 Architectures = %v, want [x86_64]", awsCfg.Architectures)
	}

	// The frozen fields survive a snapshot round-trip.
	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	rv, err := restored.ListVersions(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	if rv[1].CodeSize != int64(len("small")) {
		t.Errorf("restored version 1 CodeSize = %d", rv[1].CodeSize)
	}

	rCfg, err := restored.GetVersionAWSConfig(ctx, "my-func", "1")
	if err != nil || len(rCfg.Architectures) != 1 || rCfg.Architectures[0] != "x86_64" {
		t.Errorf("restored version 1 Architectures = %v (err %v)", rCfg.Architectures, err)
	}
}

func newSettleMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAsyncSettle())), fc
}

// TestAsyncSettlePendingThenActive: under AsyncSettle a new function reports
// State Pending (StateReasonCode Creating, no LastUpdateStatus) and rejects
// Invoke/Update/PublishVersion with a conflict until creation completes.
func TestAsyncSettlePendingThenActive(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	created, err := m.CreateFunction(ctx, defaultFuncConfig())
	if err != nil {
		t.Fatal(err)
	}

	if created.State != statePending || created.StateReasonCode != "Creating" || created.LastUpdateStatus != "" {
		t.Fatalf("create = %q/%q/%q, want Pending/Creating/<empty>",
			created.State, created.StateReasonCode, created.LastUpdateStatus)
	}

	_, invErr := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"})
	_, updErr := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 10})
	_, pubErr := m.PublishVersion(ctx, "my-func", "")

	for name, err := range map[string]error{"invoke": invErr, "update": updErr, "publish": pubErr} {
		if !cerrors.IsAlreadyExists(err) || !strings.Contains(err.Error(), "'Pending'") {
			t.Errorf("%s while Pending: err = %v, want ResourceConflict naming Pending", name, err)
		}
	}

	fc.Advance(2 * time.Second)

	got, err := m.GetFunction(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	if got.State != stateActive || got.LastUpdateStatus != lastUpdateSuccessful || got.StateReasonCode != "" {
		t.Fatalf("settled = %q/%q/%q, want Active/Successful/<empty>", got.State, got.LastUpdateStatus, got.StateReasonCode)
	}
}

// TestAsyncSettleUpdateInProgress: an update reports LastUpdateStatus
// InProgress (State stays Active); a second update, PublishVersion and
// TagResource conflict, but Invoke still works.
func TestAsyncSettleUpdateInProgress(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	fc.Advance(2 * time.Second)

	upd, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 10})
	if err != nil {
		t.Fatal(err)
	}

	if upd.State != stateActive || upd.LastUpdateStatus != lastUpdateInProgress {
		t.Fatalf("update = %q/%q, want Active/InProgress", upd.State, upd.LastUpdateStatus)
	}

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 11}); !cerrors.IsAlreadyExists(err) {
		t.Errorf("second update err = %v, want conflict", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", ""); !cerrors.IsAlreadyExists(err) {
		t.Errorf("publish err = %v, want conflict", err)
	}

	if err := m.TagFunction(ctx, "my-func", map[string]string{"a": "b"}); !cerrors.IsAlreadyExists(err) {
		t.Errorf("tag err = %v, want conflict", err)
	}

	// PublishVersionOnWrite (Publish=true on the write) is not blocked.
	if _, err := m.PublishVersionOnWrite(ctx, "my-func", ""); err != nil {
		t.Errorf("publish-on-write err = %v", err)
	}

	if _, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"}); err != nil {
		t.Errorf("invoke during update err = %v, want nil", err)
	}

	fc.Advance(2 * time.Second)

	got, err := m.GetFunction(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	if got.LastUpdateStatus != lastUpdateSuccessful {
		t.Fatalf("settled LastUpdateStatus = %q, want Successful", got.LastUpdateStatus)
	}
}

// TestDefaultSettleIsImmediate: without AsyncSettle nothing changes: Active /
// Successful right away and back-to-back updates succeed.
func TestDefaultSettleIsImmediate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	created, err := m.CreateFunction(ctx, defaultFuncConfig())
	if err != nil {
		t.Fatal(err)
	}

	if created.State != stateActive || created.LastUpdateStatus != lastUpdateSuccessful {
		t.Fatalf("create = %q/%q", created.State, created.LastUpdateStatus)
	}

	for i := 0; i < 2; i++ {
		if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 10 + i}); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
}

// TestInvokeReturnsLogTail: every invocation renders its execution log; the
// tail carries the START/END/REPORT lines and the handler error.
func TestInvokeReturnsLogTail(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	out, err := m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"START RequestId: ", "Version: $LATEST", "END RequestId: ", "REPORT RequestId: "} {
		if !strings.Contains(out.LogTail, want) {
			t.Errorf("LogTail missing %q:\n%s", want, out.LogTail)
		}
	}

	m.RegisterHandler("my-func", func(context.Context, []byte) ([]byte, error) {
		return nil, cerrors.New(cerrors.Internal, "boom")
	})

	out, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.LogTail, "[ERROR]") || !strings.Contains(out.LogTail, "boom") {
		t.Errorf("error LogTail = %q", out.LogTail)
	}
}

func TestLogTailKeepsLast4KB(t *testing.T) {
	lines := []string{strings.Repeat("a", 5000), "END"}

	got := logTail(lines)
	if len(got) != logTailBytes || !strings.HasSuffix(got, "END\n") {
		t.Fatalf("tail len = %d, suffix ok = %v", len(got), strings.HasSuffix(got, "END\n"))
	}
}

// TestGetAccountSettings reports the default limits and live usage.
func TestGetAccountSettings(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg := defaultFuncConfig()
	cfg.Code = []byte("12345")

	if _, err := m.CreateFunction(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", ""); err != nil {
		t.Fatal(err)
	}

	if err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
		FunctionName: "my-func", ReservedConcurrentExecutions: 50,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := m.GetAccountSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if s.ConcurrentExecutions != 1000 || s.UnreservedConcurrentExecutions != 950 {
		t.Errorf("concurrency = %d/%d, want 1000/950", s.ConcurrentExecutions, s.UnreservedConcurrentExecutions)
	}

	if s.CodeSizeZipped != 52428800 || s.CodeSizeUnzipped != 262144000 {
		t.Errorf("code size limits = %d/%d", s.CodeSizeZipped, s.CodeSizeUnzipped)
	}

	if s.FunctionCount != 1 || s.TotalCodeSizeUsed != 10 {
		t.Errorf("usage = %d functions / %d bytes, want 1 / 10 ($LATEST + version 1)", s.FunctionCount, s.TotalCodeSizeUsed)
	}
}

// TestCodeSigningConfigLifecycle covers CRUD, association and the in-use
// delete conflict, plus snapshot persistence.
func TestCodeSigningConfigLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateCodeSigningConfig(ctx, driver.CodeSigningConfig{}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("create without publishers err = %v, want InvalidArgument", err)
	}

	csc, err := m.CreateCodeSigningConfig(ctx, driver.CodeSigningConfig{
		Description:               "d",
		SigningProfileVersionARNs: []string{"arn:aws:signer:us-east-1:123456789012:/signing-profiles/p/abc"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(csc.ID, "csc-") || len(csc.ID) != len("csc-")+17 ||
		csc.ARN != "arn:aws:lambda:us-east-1:123456789012:code-signing-config:"+csc.ID {
		t.Fatalf("id/arn = %q / %q", csc.ID, csc.ARN)
	}

	if csc.UntrustedArtifactOnDeployment != "Warn" {
		t.Errorf("default policy = %q, want Warn", csc.UntrustedArtifactOnDeployment)
	}

	enforce := "Enforce"

	upd, err := m.UpdateCodeSigningConfig(ctx, csc.ARN, driver.CodeSigningConfigUpdate{UntrustedArtifactOnDeployment: &enforce})
	if err != nil || upd.UntrustedArtifactOnDeployment != "Enforce" || upd.Description != "d" {
		t.Fatalf("update = %+v, %v", upd, err)
	}

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	if err := m.PutFunctionCodeSigningConfig(ctx, "my-func", "arn:aws:lambda:us-east-1:123456789012:code-signing-config:csc-00000000000000000"); !cerrors.IsNotFound(err) {
		t.Fatalf("put unknown csc err = %v", err)
	}

	if err := m.PutFunctionCodeSigningConfig(ctx, "my-func", csc.ARN); err != nil {
		t.Fatal(err)
	}

	fns, err := m.ListFunctionsByCodeSigningConfig(ctx, csc.ARN)
	if err != nil || len(fns) != 1 || !strings.HasSuffix(fns[0], ":function:my-func") {
		t.Fatalf("list functions = %v, %v", fns, err)
	}

	if err := m.DeleteCodeSigningConfig(ctx, csc.ARN); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("delete in-use err = %v, want conflict", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	if got, _ := restored.GetFunctionCodeSigningConfig(ctx, "my-func"); got != csc.ARN {
		t.Errorf("restored association = %q", got)
	}

	if _, err := restored.GetCodeSigningConfig(ctx, csc.ARN); err != nil {
		t.Errorf("restored csc: %v", err)
	}

	if err := m.DeleteFunctionCodeSigningConfig(ctx, "my-func"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteCodeSigningConfig(ctx, csc.ARN); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetCodeSigningConfig(ctx, csc.ARN); !cerrors.IsNotFound(err) {
		t.Fatalf("get deleted err = %v", err)
	}

	list, _ := m.ListCodeSigningConfigs(ctx)
	if len(list) != 0 {
		t.Fatalf("list after delete = %d", len(list))
	}

	_ = json.Valid(snap)
}

// TestFunctionNotFoundMessageNamesARN matches real Lambda's
// "Function not found: <function ARN>[:<qualifier>]".
func TestFunctionNotFoundMessageNamesARN(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.GetFunction(ctx, "nope")
	if got := cerrors.Message(err); got != "Function not found: arn:aws:lambda:us-east-1:123456789012:function:nope" {
		t.Errorf("GetFunction message = %q", got)
	}

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Qualifier: "PROD"})
	if got := cerrors.Message(err); got != "Function not found: arn:aws:lambda:us-east-1:123456789012:function:my-func:PROD" {
		t.Errorf("Invoke qualifier message = %q", got)
	}
}

// TestGetAliasNotFoundMessage matches real Lambda's GetAlias text for a missing
// alias: "Cannot find alias arn: <function ARN>:<alias>".
func TestGetAliasNotFoundMessage(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	_, err := m.GetAlias(ctx, "my-func", "current")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}

	if got, want := cerrors.Message(err),
		"Cannot find alias arn: arn:aws:lambda:us-east-1:123456789012:function:my-func:current"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestPublishedVersionReportsItsOwnLifecycle: a published version reports its
// own State/LastUpdateStatus (Active/Successful), never $LATEST's in-flight
// update; only the version CreateFunction(Publish=true) cuts is Pending, and
// only until creation completes.
func TestPublishedVersionReportsItsOwnLifecycle(t *testing.T) {
	ctx := context.Background()
	m, fc := newSettleMock()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatal(err)
	}

	onCreate, err := m.PublishVersionOnWrite(ctx, "my-func", "")
	if err != nil {
		t.Fatal(err)
	}

	if onCreate.State != statePending || onCreate.StateReasonCode != stateReasonCodeCreating || onCreate.LastUpdateStatus != "" {
		t.Fatalf("version cut on create = %q/%q/%q, want Pending/Creating/<empty>",
			onCreate.State, onCreate.StateReasonCode, onCreate.LastUpdateStatus)
	}

	fc.Advance(2 * time.Second)

	if _, err := m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 10}); err != nil {
		t.Fatal(err)
	}

	vers, err := m.ListVersions(ctx, "my-func")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range vers {
		if v.Version != "1" {
			continue
		}

		if v.State != stateActive || v.StateReasonCode != "" || v.LastUpdateStatus != lastUpdateSuccessful {
			t.Errorf("version 1 during $LATEST update = %q/%q/%q, want Active/<empty>/Successful",
				v.State, v.StateReasonCode, v.LastUpdateStatus)
		}

		if v.CreatedAt != "2025-01-01T00:00:00Z" {
			t.Errorf("version 1 CreatedAt = %q, want its publish time", v.CreatedAt)
		}
	}
}
