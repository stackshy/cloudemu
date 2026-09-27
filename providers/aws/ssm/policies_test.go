package ssm_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

var policyEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed test time

func newPolicyMock() (*ssm.Mock, *config.FakeClock) {
	fc := config.NewFakeClock(policyEpoch)
	return ssm.New(config.NewOptions(config.WithClock(fc))), fc
}

func expirationPolicy(at time.Time) string {
	return `{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"` + at.Format(time.RFC3339) + `"}}`
}

func policies(ps ...string) *string {
	s := "[" + strings.Join(ps, ",") + "]"
	return &s
}

func putAdvanced(t *testing.T, m *ssm.Mock, name string, pols *string) {
	t.Helper()

	if _, _, err := m.PutParameter(context.Background(), driver.PutConfig{
		Name: name, Value: "v", Type: driver.TypeString, Tier: "Advanced", Policies: pols,
	}); err != nil {
		t.Fatalf("PutParameter(%s): %v", name, err)
	}
}

func describedPolicies(t *testing.T, m *ssm.Mock, name string) []driver.ParameterPolicy {
	t.Helper()

	metas, err := m.DescribeParameters(context.Background())
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	for _, md := range metas {
		if md.Name == name {
			return md.Policies
		}
	}

	t.Fatalf("parameter %q not described", name)

	return nil
}

func TestPolicyValidationMatrix(t *testing.T) {
	future := expirationPolicy(policyEpoch.Add(time.Hour))
	notify := `{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"1","Unit":"Hours"}}`

	eleven := make([]string, 0, 11)
	for range 11 {
		eleven = append(eleven, notify)
	}

	cases := []struct {
		name string
		pols string
		want error
	}{
		{"not an array", `{"Type":"Expiration"}`, nil},
		{"eleven policies", *policies(eleven...), driver.ErrPoliciesLimitExceeded},
		{"unknown type", `[{"Type":"Deletion","Version":"1.0","Attributes":{}}]`, driver.ErrInvalidPolicyType},
		{"missing type", `[{"Version":"1.0","Attributes":{}}]`, driver.ErrInvalidPolicyType},
		{"bad version", `[{"Type":"Expiration","Version":"2.0","Attributes":{"Timestamp":"2027-01-01T00:00:00Z"}}]`,
			driver.ErrInvalidPolicyAttribute},
		{"bad unit", `[{"Type":"NoChangeNotification","Version":"1.0","Attributes":{"After":"2","Unit":"Weeks"}}]`,
			driver.ErrInvalidPolicyAttribute},
		{"non-integer before", `[{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"x","Unit":"Days"}}]`,
			driver.ErrInvalidPolicyAttribute},
		{"past timestamp", *policies(expirationPolicy(policyEpoch.Add(-time.Hour))), driver.ErrInvalidPolicyAttribute},
		{"bad timestamp", `[{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"tomorrow"}}]`,
			driver.ErrInvalidPolicyAttribute},
		{"unknown attribute", `[{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"2027-01-01T00:00:00Z","X":"1"}}]`,
			driver.ErrInvalidPolicyAttribute},
		{"two expirations", *policies(future, future), driver.ErrIncompatiblePolicy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newPolicyMock()
			pols := tc.pols

			_, _, err := m.PutParameter(context.Background(), driver.PutConfig{
				Name: "/p", Value: "v", Type: driver.TypeString, Tier: "Advanced", Policies: &pols,
			})
			if err == nil {
				t.Fatal("PutParameter accepted invalid policies")
			}

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPolicyOffsetTimestampAndSeveralNotificationsAccepted(t *testing.T) {
	m, _ := newPolicyMock()
	notify := func(n string) string {
		return `{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"` + n + `","Unit":"Days"}}`
	}

	putAdvanced(t, m, "/ok", policies(
		`{"Type":"Expiration","Version":"1.0","Attributes":{"Timestamp":"2026-11-01T22:13:48.87+10:30:00"}}`,
		notify("30"), notify("15"),
		`{"Type":"NoChangeNotification","Version":"1.0","Attributes":{"After":"20","Unit":"Days"}}`))

	got := describedPolicies(t, m, "/ok")
	if len(got) != 4 || got[0].Type != "Expiration" || got[0].Status != "Pending" {
		t.Fatalf("policies = %+v", got)
	}

	if !strings.HasPrefix(got[0].Text, `{"Type":"Expiration","Version":"1.0"`) {
		t.Fatalf("policy text = %q", got[0].Text)
	}
}

func TestPolicyTierRules(t *testing.T) {
	ctx := context.Background()
	pol := policies(expirationPolicy(policyEpoch.Add(time.Hour)))

	m, _ := newPolicyMock()

	for _, tier := range []string{"", "Standard"} {
		_, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/std" + tier, Value: "v", Tier: tier, Policies: pol})
		if !errors.Is(err, driver.ErrPoliciesRequireAdvanced) {
			t.Fatalf("tier %q with policies: err = %v, want ErrPoliciesRequireAdvanced", tier, err)
		}
	}

	checkTier := func(name, tier, value string, pols *string, want string) {
		t.Helper()

		_, got, err := m.PutParameter(ctx, driver.PutConfig{
			Name: name, Value: value, Tier: tier, Policies: pols, Overwrite: true,
		})
		if err != nil || got != want {
			t.Fatalf("%s tier %q: got %q, %v; want %q", name, tier, got, err, want)
		}
	}

	checkTier("/it-pol", "Intelligent-Tiering", "v", pol, "Advanced")
	checkTier("/it-plain", "Intelligent-Tiering", "v", nil, "Standard")
	checkTier("/it-big", "Intelligent-Tiering", strings.Repeat("x", 5000), nil, "Advanced")
	checkTier("/adv", "Advanced", "v", nil, "Advanced")
	// An Advanced parameter never goes back to Standard.
	checkTier("/adv", "Intelligent-Tiering", "small", nil, "Advanced")

	if err := m.UpdateServiceSetting(ctx, "/ssm/parameter-store/default-parameter-tier", "Advanced"); err != nil {
		t.Fatalf("UpdateServiceSetting: %v", err)
	}

	checkTier("/default-adv", "", "v", pol, "Advanced")
}

func TestPoliciesPreservedClearedReplacedOnOverwrite(t *testing.T) {
	ctx := context.Background()
	m, _ := newPolicyMock()
	putAdvanced(t, m, "/o", policies(expirationPolicy(policyEpoch.Add(time.Hour))))

	overwrite := func(pols *string) {
		t.Helper()

		if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/o", Value: "v2", Overwrite: true, Policies: pols}); err != nil {
			t.Fatalf("overwrite: %v", err)
		}
	}

	overwrite(nil)

	if got := describedPolicies(t, m, "/o"); len(got) != 1 {
		t.Fatalf("overwrite without Policies: policies = %+v, want the kept one", got)
	}

	noChange := `{"Type":"NoChangeNotification","Version":"1.0","Attributes":{"After":"1","Unit":"Days"}}`
	overwrite(policies(noChange))

	if got := describedPolicies(t, m, "/o"); len(got) != 1 || got[0].Type != "NoChangeNotification" {
		t.Fatalf("re-sent policies: %+v, want only NoChangeNotification", got)
	}

	for _, empty := range []string{"[{}]", "[]"} {
		overwrite(policies(noChange))

		e := empty
		overwrite(&e)

		if got := describedPolicies(t, m, "/o"); len(got) != 0 {
			t.Fatalf("%s: policies = %+v, want none", empty, got)
		}
	}
}

func TestPolicyLazyExpiry(t *testing.T) {
	ctx := context.Background()
	m, fc := newPolicyMock()
	putAdvanced(t, m, "/lazy", policies(expirationPolicy(policyEpoch.Add(time.Hour))))

	if _, err := m.GetParameter(ctx, "/lazy", false); err != nil {
		t.Fatalf("GetParameter before expiry: %v", err)
	}

	fc.Advance(time.Hour)

	if _, err := m.GetParameter(ctx, "/lazy", false); err == nil {
		t.Fatal("GetParameter after expiry succeeded, want NotFound")
	}

	metas, _ := m.DescribeParameters(ctx)
	if len(metas) != 0 {
		t.Fatalf("DescribeParameters after expiry = %d params, want 0", len(metas))
	}

	// The name is free again.
	putAdvanced(t, m, "/lazy", nil)
}

// recordingPublisher captures published events. onPublish, when set, runs
// inside PublishServiceEvent.
type recordingPublisher struct {
	mu        sync.Mutex
	events    []map[string]string
	resources [][]string
	onPublish func()
}

func (r *recordingPublisher) PublishServiceEvent(_ context.Context, source, detailType string, detail any, resources []string) {
	if detailType != "Parameter Store Policy Action" {
		return
	}

	body, _ := json.Marshal(detail)

	var d map[string]string
	_ = json.Unmarshal(body, &d)
	d["source"] = source

	r.mu.Lock()
	r.events = append(r.events, d)
	r.resources = append(r.resources, resources)
	r.mu.Unlock()

	if r.onPublish != nil {
		r.onPublish()
	}
}

func TestPolicyTickFiresNotificationsAndExpiry(t *testing.T) {
	ctx := context.Background()
	m, fc := newPolicyMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	putAdvanced(t, m, "/tick", policies(
		expirationPolicy(policyEpoch.Add(48*time.Hour)),
		`{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"1","Unit":"Days"}}`))

	if m.Tick(policyEpoch.Add(time.Hour)) {
		t.Fatal("Tick before anything is due reported a change")
	}

	if m.Tick(policyEpoch.Add(24*time.Hour)) != true || len(rec.events) != 1 {
		t.Fatalf("notification tick: events = %v", rec.events)
	}

	ev := rec.events[0]
	if ev["source"] != "aws.ssm" || ev["parameter-name"] != "/tick" || ev["parameter-type"] != "String" ||
		ev["policy-type"] != "ExpirationNotification" || ev["action-status"] != "Finished" || ev["action-reason"] == "" {
		t.Fatalf("notification detail = %v", ev)
	}

	if !strings.HasSuffix(rec.resources[0][0], ":parameter/tick") {
		t.Fatalf("resources = %v", rec.resources[0])
	}

	got := describedPolicies(t, m, "/tick")
	if got[0].Status != "Pending" || got[1].Status != "Finished" {
		t.Fatalf("statuses after notification = %+v", got)
	}

	// The notification fires once.
	if m.Tick(policyEpoch.Add(25 * time.Hour)) {
		t.Fatal("second tick before expiry reported a change")
	}

	fc.Set(policyEpoch.Add(47 * time.Hour))

	if !m.Tick(policyEpoch.Add(48*time.Hour)) || len(rec.events) != 2 || rec.events[1]["policy-type"] != "Expiration" {
		t.Fatalf("expiry tick: events = %v", rec.events)
	}

	if _, err := m.GetParameter(ctx, "/tick", false); err == nil {
		t.Fatal("parameter still readable after expiry tick")
	}
}

func TestPolicyNoChangeNotificationResetsOnOverwrite(t *testing.T) {
	ctx := context.Background()
	m, fc := newPolicyMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	putAdvanced(t, m, "/nc", policies(
		`{"Type":"NoChangeNotification","Version":"1.0","Attributes":{"After":"2","Unit":"Hours"}}`))

	if !m.Tick(policyEpoch.Add(2*time.Hour)) || len(rec.events) != 1 || rec.events[0]["policy-type"] != "NoChangeNotification" {
		t.Fatalf("no-change tick: events = %v", rec.events)
	}

	if m.Tick(policyEpoch.Add(3 * time.Hour)) {
		t.Fatal("NoChangeNotification fired twice")
	}

	fc.Set(policyEpoch.Add(3 * time.Hour))

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/nc", Value: "v2", Overwrite: true}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	history, err := m.GetParameterHistory(ctx, "/nc", false)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %v, %v", history, err)
	}

	// The kept policy is copied, so version 1 keeps the status it had and
	// only version 2 starts over.
	for i, want := range []string{"Finished", "Pending"} {
		if len(history[i].Policies) != 1 || history[i].Policies[0].Status != want {
			t.Fatalf("version %d policies = %+v, want status %s", history[i].Version, history[i].Policies, want)
		}
	}

	if m.Tick(policyEpoch.Add(4 * time.Hour)) {
		t.Fatal("NoChangeNotification fired before its period restarted")
	}

	if !m.Tick(policyEpoch.Add(5*time.Hour)) || len(rec.events) != 2 {
		t.Fatalf("second period: events = %v", rec.events)
	}
}

// TestPolicyEventsPublishedOutsideLocks runs a rule target that writes back
// into the mock while the tick publishes. A publish under a lock deadlocks.
func TestPolicyEventsPublishedOutsideLocks(t *testing.T) {
	ctx := context.Background()
	m, _ := newPolicyMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	rec.onPublish = func() {
		_, _, _ = m.PutParameter(ctx, driver.PutConfig{Name: "/reentrant", Value: "again", Tier: "Advanced", Overwrite: true})
		_, _, _ = m.PutParameter(ctx, driver.PutConfig{Name: "/other", Value: "x", Overwrite: true})
		_, _ = m.DescribeParameters(ctx)
	}

	putAdvanced(t, m, "/reentrant", policies(
		expirationPolicy(policyEpoch.Add(2*time.Hour)),
		`{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"1","Unit":"Hours"}}`))

	// The notification fires on a live parameter, so the target's overwrite
	// takes the same parameter lock. The expiry tick then re-creates it.
	for _, at := range []time.Duration{time.Hour, 2 * time.Hour} {
		done := make(chan bool)

		go func() { done <- m.Tick(policyEpoch.Add(at)) }()

		select {
		case changed := <-done:
			if !changed {
				t.Fatalf("Tick at +%v reported no change", at)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Tick at +%v deadlocked while a rule target called PutParameter", at)
		}
	}

	// The target re-created the expired parameter, without policies.
	p, err := m.GetParameter(ctx, "/reentrant", false)
	if err != nil || p.Value != "again" {
		t.Fatalf("re-created parameter = %+v, %v", p, err)
	}
}

func TestServiceSettings(t *testing.T) {
	ctx := context.Background()
	m, _ := newPolicyMock()

	const tierID = "/ssm/parameter-store/default-parameter-tier"

	s, err := m.GetServiceSetting(ctx, tierID)
	if err != nil || s.SettingValue != "Standard" || s.Status != "Default" || s.LastModifiedUser != "System" ||
		s.ARN != "arn:aws:ssm:us-east-1:123456789012:servicesetting/ssm/parameter-store/default-parameter-tier" {
		t.Fatalf("default setting = %+v, %v", s, err)
	}

	if err := m.UpdateServiceSetting(ctx, s.ARN, "Intelligent-Tiering"); err != nil {
		t.Fatalf("update by ARN: %v", err)
	}

	s, _ = m.GetServiceSetting(ctx, tierID)
	if s.SettingValue != "Intelligent-Tiering" || s.Status != "Customized" {
		t.Fatalf("customized setting = %+v", s)
	}

	s, err = m.ResetServiceSetting(ctx, tierID)
	if err != nil || s.SettingValue != "Standard" || s.Status != "Default" {
		t.Fatalf("reset setting = %+v, %v", s, err)
	}

	const throughputID = "/ssm/parameter-store/high-throughput-enabled"

	if err := m.UpdateServiceSetting(ctx, throughputID, "maybe"); err == nil {
		t.Fatal("invalid value accepted")
	}

	if err := m.UpdateServiceSetting(ctx, throughputID, "true"); err != nil {
		t.Fatalf("update throughput: %v", err)
	}

	for _, bad := range []string{"/ssm/nope", "arn:aws:ssm:eu-west-1:123456789012:servicesetting" + tierID} {
		if _, err := m.GetServiceSetting(ctx, bad); !errors.Is(err, driver.ErrServiceSettingNotFound) {
			t.Fatalf("GetServiceSetting(%s) err = %v, want ErrServiceSettingNotFound", bad, err)
		}
	}
}

func TestSnapshotKeepsPoliciesAndSettings(t *testing.T) {
	ctx := context.Background()
	src, _ := newPolicyMock()
	putAdvanced(t, src, "/snap", policies(
		expirationPolicy(policyEpoch.Add(48*time.Hour)),
		`{"Type":"ExpirationNotification","Version":"1.0","Attributes":{"Before":"1","Unit":"Days"}}`))
	src.Tick(policyEpoch.Add(24 * time.Hour))

	if err := src.UpdateServiceSetting(ctx, "/ssm/parameter-store/default-parameter-tier", "Advanced"); err != nil {
		t.Fatalf("UpdateServiceSetting: %v", err)
	}

	raw, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst, _ := newPolicyMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := describedPolicies(t, dst, "/snap")
	if len(got) != 2 || got[0].Status != "Pending" || got[1].Status != "Finished" {
		t.Fatalf("restored policies = %+v", got)
	}

	s, _ := dst.GetServiceSetting(ctx, "/ssm/parameter-store/default-parameter-tier")
	if s.SettingValue != "Advanced" {
		t.Fatalf("restored setting = %+v", s)
	}

	if !dst.Tick(policyEpoch.Add(48 * time.Hour)) {
		t.Fatal("restored Expiration did not fire")
	}
}

// countingPublisher counts Expiration events.
type countingPublisher struct {
	mu          sync.Mutex
	expirations int
}

func (c *countingPublisher) PublishServiceEvent(_ context.Context, _, _ string, detail any, _ []string) {
	body, _ := json.Marshal(detail)

	var d map[string]string
	_ = json.Unmarshal(body, &d)

	if d["policy-type"] == "Expiration" {
		c.mu.Lock()
		c.expirations++
		c.mu.Unlock()
	}
}

// TestPolicyExpirationPublishedOnce races ticks and lazy reads on an expired
// parameter. Only the first caller may publish the Expiration event.
func TestPolicyExpirationPublishedOnce(t *testing.T) {
	const workers = 8

	for range 50 {
		m, fc := newPolicyMock()
		pub := &countingPublisher{}
		m.SetEventPublisher(pub)
		putAdvanced(t, m, "/once", policies(expirationPolicy(policyEpoch.Add(time.Hour))))
		fc.Set(policyEpoch.Add(time.Hour))

		var wg sync.WaitGroup

		for i := range workers {
			wg.Add(1)

			go func() {
				defer wg.Done()

				if i%2 == 0 {
					m.Tick(policyEpoch.Add(time.Hour))
				} else {
					_, _ = m.GetParameter(context.Background(), "/once", false)
				}
			}()
		}

		wg.Wait()

		if pub.expirations != 1 {
			t.Fatalf("Expiration events = %d, want exactly 1", pub.expirations)
		}
	}
}

// TestPolicyOverwriteRacingExpiryKeepsTheWrite races an overwrite that
// clears the policies against the tick that expires the parameter. Either
// order must leave the written value readable.
func TestPolicyOverwriteRacingExpiryKeepsTheWrite(t *testing.T) {
	ctx := context.Background()
	clear := "[]"

	for range 300 {
		m, _ := newPolicyMock()
		putAdvanced(t, m, "/race", policies(expirationPolicy(policyEpoch.Add(time.Hour))))

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			m.Tick(policyEpoch.Add(time.Hour))
		}()

		go func() {
			defer wg.Done()

			if _, _, err := m.PutParameter(ctx, driver.PutConfig{
				Name: "/race", Value: "new", Tier: "Advanced", Overwrite: true, Policies: &clear,
			}); err != nil {
				t.Errorf("PutParameter: %v", err)
			}
		}()

		wg.Wait()

		p, err := m.GetParameter(ctx, "/race", false)
		if err != nil || p.Value != "new" {
			t.Fatalf("after the race: %+v, %v; want the written value", p, err)
		}
	}
}
