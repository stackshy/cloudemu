package apigateway_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const parallel = 50

// race runs fn from parallel goroutines at once and returns how many returned nil.
func race(fn func() error) (ok int64) {
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)

	for i := 0; i < parallel; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			if fn() == nil {
				atomic.AddInt64(&ok, 1)
			}
		}()
	}

	close(start)
	wg.Wait()

	return ok
}

func TestParallelCreatesAreAtomic(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	value := "0123456789012345678901234567890123456789"

	if got := race(func() error {
		_, err := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Value: value})

		return err
	}); got != 1 {
		t.Fatalf("API key with one value: %d creates succeeded, want 1", got)
	}

	if got := race(func() error {
		_, err := m.CreateDomainName(ctx(), &driver.CreateDomainNameInput{DomainName: "d.example.com", CertificateARN: "arn"})

		return err
	}); got != 1 {
		t.Fatalf("domain name: %d creates succeeded, want 1", got)
	}

	if got := race(func() error {
		_, err := m.CreateModel(ctx(), api.ID, &driver.CreateModelInput{Name: "Same", Schema: "{}"})

		return err
	}); got != 1 {
		t.Fatalf("model name: %d creates succeeded, want 1", got)
	}

	_, _ = m.CreateBasePathMapping(ctx(), "d.example.com", driver.BasePathMapping{RestAPIID: api.ID})
	deployed, stage := deployedStage(t, m)

	if got := race(func() error {
		_, err := m.CreateBasePathMapping(ctx(), "d.example.com", driver.BasePathMapping{BasePath: "v1", RestAPIID: deployed, Stage: stage})

		return err
	}); got != 1 {
		t.Fatalf("base path mapping: %d creates succeeded, want 1", got)
	}
}

func TestParallelRequestsRespectTheQuota(t *testing.T) {
	f := newDP(t)
	f.method(t, driver.PutMethodInput{APIKeyRequired: true}, nil)

	key, _ := f.m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Enabled: true})
	plan, _ := f.m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "p", APIStages: []driver.UsagePlanStage{{RestAPIID: f.api.ID, Stage: "prod"}},
		Quota: &driver.QuotaSettings{Limit: 20, Period: "DAY"},
	})
	_, _ = f.m.CreateUsagePlanKey(ctx(), plan.ID, key.ID, "API_KEY")

	hdr := map[string]string{"x-api-key": key.Value}

	f.m.SetLambdaInvoker(staticInvoker{})

	got := race(func() error {
		resp, err := f.m.InvokeRoute(ctx(), &driver.ProxyRequest{
			RestAPIID: f.api.ID, StageName: "prod", HTTPMethod: "GET", Path: "/items", Headers: hdr,
		})
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return errors.New(errors.FailedPrecondition, "throttled")
		}

		return nil
	})
	if got != 20 {
		t.Fatalf("%d requests passed a quota of 20", got)
	}
}

// staticInvoker is a stateless Lambda stand-in, safe to call from many goroutines.
type staticInvoker struct{}

func (staticInvoker) InvokeSync(context.Context, string, []byte) ([]byte, string, error) {
	return []byte(`{"statusCode":200,"body":"ok"}`), "", nil
}

// TestReadsAreDeepCopies mutates every returned map and slice; the stored values
// must not change.
func TestReadsAreDeepCopies(t *testing.T) {
	m := newMock(t)
	api := newAPI(t, m)

	key, _ := m.CreateAPIKey(ctx(), &driver.CreateAPIKeyInput{Name: "k", Tags: map[string]string{"a": "b"}})
	plan, _ := m.CreateUsagePlan(ctx(), &driver.CreateUsagePlanInput{
		Name: "p", Tags: map[string]string{"a": "b"}, APIStages: []driver.UsagePlanStage{{RestAPIID: "x", Stage: "y"}}[:0],
	})
	az, _ := m.CreateAuthorizer(ctx(), api.ID, &driver.CreateAuthorizerInput{
		Name: "a", Type: driver.AuthorizerCognito, ProviderARNs: []string{"arn:aws:cognito-idp:us-east-1:000000000000:userpool/p"},
	})
	_, _ = m.PutGatewayResponse(ctx(), api.ID, "THROTTLED", &driver.PutGatewayResponseInput{ResponseTemplates: map[string]string{"a": "b"}})

	k1, _ := m.GetAPIKey(ctx(), key.ID, true)
	k1.Tags["a"], k1.StageKeys = "mutated", append(k1.StageKeys, "x")

	if k2, _ := m.GetAPIKey(ctx(), key.ID, true); k2.Tags["a"] != "b" || len(k2.StageKeys) != 0 {
		t.Fatalf("api key aliased: %+v", k2)
	}

	p1, _ := m.GetUsagePlan(ctx(), plan.ID)
	p1.Tags["a"] = "mutated"

	if p2, _ := m.GetUsagePlan(ctx(), plan.ID); p2.Tags["a"] != "b" {
		t.Fatalf("usage plan aliased: %+v", p2)
	}

	a1, _ := m.GetAuthorizer(ctx(), api.ID, az.ID)
	a1.ProviderARNs[0] = "mutated"

	if a2, _ := m.GetAuthorizer(ctx(), api.ID, az.ID); a2.ProviderARNs[0] == "mutated" {
		t.Fatal("authorizer aliased")
	}

	g1, _ := m.GetGatewayResponse(ctx(), api.ID, "THROTTLED")
	g1.ResponseTemplates["a"] = "mutated"

	if g2, _ := m.GetGatewayResponse(ctx(), api.ID, "THROTTLED"); g2.ResponseTemplates["a"] != "b" {
		t.Fatal("gateway response aliased")
	}
}
