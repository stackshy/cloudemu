package lambda

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

func TestFunctionURLConfigRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	created, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func",
		AuthType:     "NONE",
		Cors:         &driver.FunctionURLCors{AllowOrigins: []string{"*"}},
	})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig: %v", err)
	}

	if !strings.HasPrefix(created.FunctionURL, "https://") || !strings.Contains(created.FunctionURL, ".lambda-url.us-east-1.on.aws/") {
		t.Fatalf("FunctionURL = %q, want the real Lambda Function URL shape", created.FunctionURL)
	}

	if created.InvokeMode != defaultInvokeMode {
		t.Fatalf("InvokeMode = %q, want default %q", created.InvokeMode, defaultInvokeMode)
	}

	// $LATEST/unqualified: FunctionArn carries no trailing qualifier suffix.
	if strings.Contains(created.FunctionArn, ":$LATEST") {
		t.Fatalf("FunctionArn = %q, unexpected $LATEST qualifier suffix", created.FunctionArn)
	}

	got, err := m.GetFunctionURLConfig(ctx, "my-func", "")
	if err != nil {
		t.Fatalf("GetFunctionURLConfig: %v", err)
	}

	if got.FunctionURL != created.FunctionURL {
		t.Fatalf("Get FunctionURL = %q, want %q", got.FunctionURL, created.FunctionURL)
	}

	// "$LATEST" and "" are the same qualifier.
	gotLatest, err := m.GetFunctionURLConfig(ctx, "my-func", latestVersion)
	if err != nil {
		t.Fatalf("GetFunctionURLConfig($LATEST): %v", err)
	}

	if gotLatest.FunctionURL != created.FunctionURL {
		t.Fatalf("$LATEST FunctionURL = %q, want %q", gotLatest.FunctionURL, created.FunctionURL)
	}

	updated, err := m.UpdateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func",
		AuthType:     authTypeAWSIAM,
	})
	if err != nil {
		t.Fatalf("UpdateFunctionURLConfig: %v", err)
	}

	if updated.AuthType != authTypeAWSIAM {
		t.Fatalf("AuthType after update = %q, want %q", updated.AuthType, authTypeAWSIAM)
	}

	if updated.Cors == nil || len(updated.Cors.AllowOrigins) != 1 {
		t.Fatalf("Cors after update = %+v, want unchanged from create", updated.Cors)
	}

	list, err := m.ListFunctionURLConfigs(ctx, "my-func")
	if err != nil {
		t.Fatalf("ListFunctionURLConfigs: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("ListFunctionURLConfigs = %d, want 1", len(list))
	}

	if err := m.DeleteFunctionURLConfig(ctx, "my-func", ""); err != nil {
		t.Fatalf("DeleteFunctionURLConfig: %v", err)
	}

	if _, err := m.GetFunctionURLConfig(ctx, "my-func", ""); !errors.IsNotFound(err) {
		t.Fatalf("Get after delete err = %v, want NotFound", err)
	}
}

func TestFunctionURLConfigPerQualifier(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := m.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "my-func", Name: "live", FunctionVersion: "1",
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	latestCfg, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "my-func"})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig($LATEST): %v", err)
	}

	aliasCfg, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "my-func", Qualifier: "live"})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig(live): %v", err)
	}

	if latestCfg.FunctionURL == aliasCfg.FunctionURL {
		t.Fatal("the $LATEST and alias URL configs got the same FunctionURL")
	}

	if aliasCfg.FunctionArn != qualifiedARN(latestCfg.FunctionArn, "live") {
		t.Fatalf("alias FunctionArn = %q, want a :live-qualified ARN", aliasCfg.FunctionArn)
	}

	list, err := m.ListFunctionURLConfigs(ctx, "my-func")
	if err != nil {
		t.Fatalf("ListFunctionURLConfigs: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("ListFunctionURLConfigs = %d, want 2 (one per qualifier)", len(list))
	}

	// Deleting the alias's URL leaves $LATEST's untouched.
	if err := m.DeleteFunctionURLConfig(ctx, "my-func", "live"); err != nil {
		t.Fatalf("DeleteFunctionURLConfig(live): %v", err)
	}

	if _, err := m.GetFunctionURLConfig(ctx, "my-func", ""); err != nil {
		t.Fatalf("GetFunctionURLConfig($LATEST) after deleting the alias's config: %v", err)
	}

	if _, err := m.GetFunctionURLConfig(ctx, "my-func", "live"); !errors.IsNotFound(err) {
		t.Fatalf("GetFunctionURLConfig(live) after delete err = %v, want NotFound", err)
	}
}

func TestFunctionURLConfigRejectsVersionQualifier(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func", Qualifier: "1",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("CreateFunctionURLConfig(qualifier=1) err = %v, want InvalidArgument", err)
	}
}

func TestFunctionURLConfigRejectsUnknownAlias(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func", Qualifier: "no-such-alias",
	}); !errors.IsNotFound(err) {
		t.Fatalf("CreateFunctionURLConfig(unknown alias) err = %v, want NotFound", err)
	}
}

func TestFunctionURLConfigDuplicateConflicts(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "my-func"}); err != nil {
		t.Fatalf("first CreateFunctionURLConfig: %v", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "my-func"}); !errors.IsAlreadyExists(err) {
		t.Fatalf("second CreateFunctionURLConfig err = %v, want AlreadyExists", err)
	}
}

func TestFunctionURLConfigInvalidAuthTypeAndInvokeMode(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func", AuthType: "BOGUS",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("CreateFunctionURLConfig(bad AuthType) err = %v, want InvalidArgument", err)
	}

	if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func", InvokeMode: "BOGUS",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("CreateFunctionURLConfig(bad InvokeMode) err = %v, want InvalidArgument", err)
	}
}

// TestFunctionURLConfigCOWIndependence confirms a caller mutating a returned
// config's Cors slices never disturbs the stored state — the copy-on-write
// contract every funcData map field in this package follows.
func TestFunctionURLConfigCOWIndependence(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	created, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
		FunctionName: "my-func",
		Cors:         &driver.FunctionURLCors{AllowOrigins: []string{"https://example.com"}},
	})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig: %v", err)
	}

	created.Cors.AllowOrigins[0] = "mutated"
	created.AuthType = "mutated"

	got, err := m.GetFunctionURLConfig(ctx, "my-func", "")
	if err != nil {
		t.Fatalf("GetFunctionURLConfig: %v", err)
	}

	if got.Cors.AllowOrigins[0] != "https://example.com" {
		t.Fatalf("stored AllowOrigins = %q, want unaffected by caller mutation", got.Cors.AllowOrigins[0])
	}

	if got.AuthType != defaultAuthType {
		t.Fatalf("stored AuthType = %q, want unaffected by caller mutation", got.AuthType)
	}
}

func TestFunctionURLConfigConcurrent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	const aliasCount = 8

	var wg sync.WaitGroup

	for i := range aliasCount {
		name := aliasName(i)

		if _, err := m.CreateAlias(ctx, driver.AliasConfig{
			FunctionName: "my-func", Name: name, FunctionVersion: "1",
		}); err != nil {
			t.Fatalf("CreateAlias(%s): %v", name, err)
		}

		wg.Add(1)

		go func(qualifier string) {
			defer wg.Done()

			if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
				FunctionName: "my-func", Qualifier: qualifier,
			}); err != nil {
				t.Errorf("CreateFunctionURLConfig(%s): %v", qualifier, err)
			}

			if _, err := m.GetFunctionURLConfig(ctx, "my-func", qualifier); err != nil {
				t.Errorf("GetFunctionURLConfig(%s): %v", qualifier, err)
			}
		}(name)
	}

	wg.Wait()

	list, err := m.ListFunctionURLConfigs(ctx, "my-func")
	if err != nil {
		t.Fatalf("ListFunctionURLConfigs: %v", err)
	}

	if len(list) != aliasCount {
		t.Fatalf("ListFunctionURLConfigs = %d, want %d", len(list), aliasCount)
	}
}

func aliasName(i int) string {
	const base = 'a'
	return "alias-" + string(rune(base+i))
}

func TestResolveFunctionURL(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	created, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: "my-func"})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig: %v", err)
	}

	parsed, err := url.Parse(created.FunctionURL)
	if err != nil {
		t.Fatalf("parse FunctionURL: %v", err)
	}

	resolved, err := m.ResolveFunctionURL(ctx, strings.ToUpper(parsed.Hostname()))
	if err != nil {
		t.Fatalf("ResolveFunctionURL: %v", err)
	}

	if resolved.FunctionName != "my-func" {
		t.Fatalf("resolved FunctionName = %q, want my-func", resolved.FunctionName)
	}

	if _, err := m.ResolveFunctionURL(ctx, "nonexistent.lambda-url.us-east-1.on.aws"); !errors.IsNotFound(err) {
		t.Fatalf("ResolveFunctionURL(unknown host) err = %v, want NotFound", err)
	}
}
