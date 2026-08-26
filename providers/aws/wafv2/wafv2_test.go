package wafv2

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func newMock() *Mock {
	return New(config.NewOptions())
}

func TestWebACLCRUDAndLockToken(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	acl, err := m.CreateWebACL(ctx, driver.CreateWebACLInput{
		Name:  "acl1",
		Scope: driver.ScopeRegional,
		Rules: json.RawMessage(`[{"Name":"r1"}]`),
	})
	if err != nil {
		t.Fatalf("CreateWebACL: %v", err)
	}

	if acl.ID == "" || acl.LockToken == "" {
		t.Fatalf("missing id/lock token: %+v", acl)
	}

	got, err := m.GetWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID})
	if err != nil {
		t.Fatalf("GetWebACL: %v", err)
	}

	if string(got.Rules) != `[{"Name":"r1"}]` {
		t.Fatalf("rules not stored verbatim: %s", got.Rules)
	}

	// Stale lock token rejected.
	if _, err := m.UpdateWebACL(ctx, driver.UpdateWebACLInput{
		Scope: driver.ScopeRegional, ID: acl.ID, LockToken: "wrong",
	}); !isOptimisticLock(err) {
		t.Fatalf("want optimistic lock error, got %v", err)
	}

	newTok, err := m.UpdateWebACL(ctx, driver.UpdateWebACLInput{
		Scope: driver.ScopeRegional, ID: acl.ID, LockToken: acl.LockToken,
		Description: "updated", Rules: json.RawMessage(`[{"Name":"r2"}]`),
	})
	if err != nil {
		t.Fatalf("UpdateWebACL: %v", err)
	}

	if newTok == acl.LockToken {
		t.Fatal("lock token did not rotate on update")
	}

	got, _ = m.GetWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID})
	if got.Description != "updated" || string(got.Rules) != `[{"Name":"r2"}]` {
		t.Fatalf("update not applied: %+v", got)
	}

	// Delete with stale token fails, current token succeeds.
	if err := m.DeleteWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}, "stale"); !isOptimisticLock(err) {
		t.Fatalf("delete stale token: want lock error, got %v", err)
	}

	if err := m.DeleteWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}, newTok); err != nil {
		t.Fatalf("DeleteWebACL: %v", err)
	}

	if _, err := m.GetWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}); !cerrors.IsNotFound(err) {
		t.Fatalf("want not found after delete, got %v", err)
	}
}

func TestWebACLDuplicateAndScopePartition(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "dup", Scope: driver.ScopeRegional}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "dup", Scope: driver.ScopeRegional}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want duplicate error, got %v", err)
	}

	// Same name in a different scope is allowed.
	if _, err := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "dup", Scope: driver.ScopeCloudFront}); err != nil {
		t.Fatalf("cross-scope create should succeed: %v", err)
	}

	reg, _ := m.ListWebACLs(ctx, driver.ScopeRegional)
	cf, _ := m.ListWebACLs(ctx, driver.ScopeCloudFront)

	if len(reg) != 1 || len(cf) != 1 {
		t.Fatalf("scope partition failed: regional=%d cloudfront=%d", len(reg), len(cf))
	}
}

func TestIPSetCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	set, err := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		Name: "ips", Scope: driver.ScopeRegional,
		IPAddressVersion: "IPV4", Addresses: []string{"1.2.3.4/32"},
	})
	if err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}

	tok, err := m.UpdateIPSet(ctx, driver.UpdateIPSetInput{
		Scope: driver.ScopeRegional, ID: set.ID, LockToken: set.LockToken,
		Addresses: []string{"5.6.7.8/32", "9.10.11.12/32"},
	})
	if err != nil {
		t.Fatalf("UpdateIPSet: %v", err)
	}

	got, _ := m.GetIPSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: set.ID})
	if len(got.Addresses) != 2 {
		t.Fatalf("addresses not updated: %+v", got.Addresses)
	}

	if err := m.DeleteIPSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: set.ID}, tok); err != nil {
		t.Fatalf("DeleteIPSet: %v", err)
	}
}

func TestRuleGroupAndRegexSetCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	grp, err := m.CreateRuleGroup(ctx, driver.CreateRuleGroupInput{
		Name: "rg", Scope: driver.ScopeRegional, Capacity: 100,
		Rules: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatalf("CreateRuleGroup: %v", err)
	}

	if _, err := m.GetRuleGroup(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: grp.ID}); err != nil {
		t.Fatalf("GetRuleGroup: %v", err)
	}

	rs, err := m.CreateRegexPatternSet(ctx, driver.CreateRegexPatternSetInput{
		Name: "rs", Scope: driver.ScopeRegional,
		RegularExpressionList: json.RawMessage(`[{"RegexString":"^a"}]`),
	})
	if err != nil {
		t.Fatalf("CreateRegexPatternSet: %v", err)
	}

	got, _ := m.GetRegexPatternSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: rs.ID})
	if string(got.RegularExpressionList) != `[{"RegexString":"^a"}]` {
		t.Fatalf("regex list not verbatim: %s", got.RegularExpressionList)
	}
}

func TestTagsAndAssociations(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	acl, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "tagacl", Scope: driver.ScopeRegional})

	if err := m.TagResource(ctx, acl.ARN, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	_, tags, err := m.ListTagsForResource(ctx, acl.ARN)
	if err != nil || tags["env"] != "prod" {
		t.Fatalf("ListTagsForResource: %v tags=%v", err, tags)
	}

	if err := m.UntagResource(ctx, acl.ARN, []string{"env"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	_, tags, _ = m.ListTagsForResource(ctx, acl.ARN)
	if len(tags) != 0 {
		t.Fatalf("tag not removed: %v", tags)
	}

	// Association lifecycle.
	resARN := "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/x/abc"
	if err := m.AssociateWebACL(ctx, acl.ARN, resARN); err != nil {
		t.Fatalf("AssociateWebACL: %v", err)
	}

	found, err := m.GetWebACLForResource(ctx, resARN)
	if err != nil || found == nil || found.ARN != acl.ARN {
		t.Fatalf("GetWebACLForResource: %v %+v", err, found)
	}

	res, _ := m.ListResourcesForWebACL(ctx, acl.ARN, "APPLICATION_LOAD_BALANCER")
	if len(res) != 1 || res[0] != resARN {
		t.Fatalf("ListResourcesForWebACL: %v", res)
	}

	if err := m.DisassociateWebACL(ctx, resARN); err != nil {
		t.Fatalf("DisassociateWebACL: %v", err)
	}

	found, _ = m.GetWebACLForResource(ctx, resARN)
	if found != nil {
		t.Fatalf("expected no association after disassociate, got %+v", found)
	}
}

func TestErrorPaths(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.GetWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: "missing"}); !cerrors.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}

	if err := m.TagResource(ctx, "arn:aws:wafv2:us-east-1:0:regional/webacl/x/y", nil); !cerrors.IsNotFound(err) {
		t.Fatalf("tag missing resource: want not found, got %v", err)
	}

	if _, err := m.CreateIPSet(ctx, driver.CreateIPSetInput{Name: "x", Scope: driver.ScopeRegional}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing IPAddressVersion: want invalid arg, got %v", err)
	}
}

func TestCheckCapacity(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Empty rule set reports zero.
	zero, err := m.CheckCapacity(ctx, driver.ScopeRegional, nil)
	if err != nil || zero != 0 {
		t.Fatalf("empty CheckCapacity: cap=%d err=%v", zero, err)
	}

	// Two rules, one with a rate-based statement and one with a managed group.
	rules := json.RawMessage(`[
		{"Statement":{"RateBasedStatement":{}}},
		{"Statement":{"ManagedRuleGroupStatement":{}}}
	]`)

	capacity, err := m.CheckCapacity(ctx, driver.ScopeRegional, rules)
	if err != nil {
		t.Fatalf("CheckCapacity: %v", err)
	}

	// 1+2 (rate) + 1+10 (managed) = 14.
	if capacity != 14 {
		t.Fatalf("want capacity 14, got %d", capacity)
	}

	if _, err := m.CheckCapacity(ctx, "", rules); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing scope: want invalid arg, got %v", err)
	}
}

func TestLoggingConfigurationLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	acl, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "logacl", Scope: driver.ScopeRegional})

	cfg := json.RawMessage(`{"ResourceArn":"` + acl.ARN + `","LogDestinationConfigs":["arn:aws:firehose:x"]}`)

	echoed, err := m.PutLoggingConfiguration(ctx, cfg)
	if err != nil || string(echoed) != string(cfg) {
		t.Fatalf("PutLoggingConfiguration: echoed=%s err=%v", echoed, err)
	}

	got, err := m.GetLoggingConfiguration(ctx, acl.ARN)
	if err != nil || string(got) != string(cfg) {
		t.Fatalf("GetLoggingConfiguration: got=%s err=%v", got, err)
	}

	list, _ := m.ListLoggingConfigurations(ctx, driver.ScopeRegional)
	if len(list) != 1 {
		t.Fatalf("want 1 logging config, got %d", len(list))
	}

	if len(mustList(m, ctx, driver.ScopeCloudFront)) != 0 {
		t.Fatal("cloudfront scope should have no logging configs")
	}

	if err := m.DeleteLoggingConfiguration(ctx, acl.ARN); err != nil {
		t.Fatalf("DeleteLoggingConfiguration: %v", err)
	}

	if _, err := m.GetLoggingConfiguration(ctx, acl.ARN); !cerrors.IsNotFound(err) {
		t.Fatalf("want not found after delete, got %v", err)
	}
}

func mustList(m *Mock, ctx context.Context, scope string) []json.RawMessage {
	out, _ := m.ListLoggingConfigurations(ctx, scope)

	return out
}

func TestPermissionPolicyLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	grp, _ := m.CreateRuleGroup(ctx, driver.CreateRuleGroupInput{Name: "polrg", Scope: driver.ScopeRegional, Capacity: 5})

	// Policy on a non-rule-group ARN fails.
	if err := m.PutPermissionPolicy(ctx, "arn:aws:wafv2:x:0:regional/rulegroup/none/z", `{"a":1}`); !cerrors.IsNotFound(err) {
		t.Fatalf("policy on missing rule group: want not found, got %v", err)
	}

	if err := m.PutPermissionPolicy(ctx, grp.ARN, `{"Version":"2012-10-17"}`); err != nil {
		t.Fatalf("PutPermissionPolicy: %v", err)
	}

	policy, err := m.GetPermissionPolicy(ctx, grp.ARN)
	if err != nil || policy != `{"Version":"2012-10-17"}` {
		t.Fatalf("GetPermissionPolicy: policy=%q err=%v", policy, err)
	}

	if err := m.DeletePermissionPolicy(ctx, grp.ARN); err != nil {
		t.Fatalf("DeletePermissionPolicy: %v", err)
	}

	if _, err := m.GetPermissionPolicy(ctx, grp.ARN); !cerrors.IsNotFound(err) {
		t.Fatalf("want not found after delete, got %v", err)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateAPIKey(ctx, driver.ScopeRegional, nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("no token domains: want invalid arg, got %v", err)
	}

	apiKey, err := m.CreateAPIKey(ctx, driver.ScopeRegional, []string{"abc.com"})
	if err != nil || apiKey == "" {
		t.Fatalf("CreateAPIKey: key=%q err=%v", apiKey, err)
	}

	keys, _ := m.ListAPIKeys(ctx, driver.ScopeRegional)
	if len(keys) != 1 || keys[0].APIKey != apiKey || keys[0].TokenDomains[0] != "abc.com" {
		t.Fatalf("ListAPIKeys: %+v", keys)
	}

	// Scope partitioning: the key is not visible in CLOUDFRONT.
	if cf, _ := m.ListAPIKeys(ctx, driver.ScopeCloudFront); len(cf) != 0 {
		t.Fatalf("cloudfront scope should have no keys, got %d", len(cf))
	}

	dec, err := m.GetDecryptedAPIKey(ctx, driver.ScopeRegional, apiKey)
	if err != nil || dec.TokenDomains[0] != "abc.com" {
		t.Fatalf("GetDecryptedAPIKey: %+v err=%v", dec, err)
	}

	if err := m.DeleteAPIKey(ctx, driver.ScopeRegional, apiKey); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	if _, err := m.GetDecryptedAPIKey(ctx, driver.ScopeRegional, apiKey); !cerrors.IsNotFound(err) {
		t.Fatalf("want not found after delete, got %v", err)
	}
}

func isOptimisticLock(err error) bool {
	var apiErr *driver.APIError

	return stderrors.As(err, &apiErr) && apiErr.Exception == driver.ExOptimisticLock
}

func isAssociatedItem(err error) bool {
	var apiErr *driver.APIError

	return stderrors.As(err, &apiErr) && apiErr.Exception == driver.ExAssociatedItem
}

func TestARNShape(t *testing.T) {
	m := New(config.NewOptions(config.WithRegion("eu-west-1")))
	ctx := context.Background()

	reg, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "r1", Scope: driver.ScopeRegional})
	// REGIONAL: lowercase "regional" scope-path segment + the configured region.
	wantReg := "arn:aws:wafv2:eu-west-1:" + m.opts.AccountID + ":regional/webacl/r1/" + reg.ID
	if reg.ARN != wantReg {
		t.Fatalf("regional ARN = %q, want %q", reg.ARN, wantReg)
	}

	cf, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "c1", Scope: driver.ScopeCloudFront})
	// CLOUDFRONT: lowercase "global" scope-path segment + region us-east-1 (never
	// the configured region, never the literal word "global" in the region slot).
	wantCF := "arn:aws:wafv2:us-east-1:" + m.opts.AccountID + ":global/webacl/c1/" + cf.ID
	if cf.ARN != wantCF {
		t.Fatalf("cloudfront ARN = %q, want %q", cf.ARN, wantCF)
	}

	// The other resource kinds follow the same shape.
	ips, _ := m.CreateIPSet(ctx, driver.CreateIPSetInput{Name: "i1", Scope: driver.ScopeRegional, IPAddressVersion: "IPV4"})
	if want := "arn:aws:wafv2:eu-west-1:" + m.opts.AccountID + ":regional/ipset/i1/" + ips.ID; ips.ARN != want {
		t.Fatalf("ipset ARN = %q, want %q", ips.ARN, want)
	}

	grp, _ := m.CreateRuleGroup(ctx, driver.CreateRuleGroupInput{Name: "g1", Scope: driver.ScopeCloudFront, Capacity: 1})
	if want := "arn:aws:wafv2:us-east-1:" + m.opts.AccountID + ":global/rulegroup/g1/" + grp.ID; grp.ARN != want {
		t.Fatalf("rulegroup ARN = %q, want %q", grp.ARN, want)
	}

	rx, _ := m.CreateRegexPatternSet(ctx, driver.CreateRegexPatternSetInput{Name: "x1", Scope: driver.ScopeRegional})
	if want := "arn:aws:wafv2:eu-west-1:" + m.opts.AccountID + ":regional/regexpatternset/x1/" + rx.ID; rx.ARN != want {
		t.Fatalf("regexset ARN = %q, want %q", rx.ARN, want)
	}
}

func TestDeleteWebACLBlockedWhileAssociated(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	acl, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "assoc", Scope: driver.ScopeRegional})
	resARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/x/abc"

	if err := m.AssociateWebACL(ctx, acl.ARN, resARN); err != nil {
		t.Fatalf("AssociateWebACL: %v", err)
	}

	// Delete with the correct lock token still fails while associated.
	if err := m.DeleteWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}, acl.LockToken); !isAssociatedItem(err) {
		t.Fatalf("delete while associated: want WAFAssociatedItemException, got %v", err)
	}

	// After disassociation the delete succeeds.
	if err := m.DisassociateWebACL(ctx, resARN); err != nil {
		t.Fatalf("DisassociateWebACL: %v", err)
	}

	if err := m.DeleteWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}, acl.LockToken); err != nil {
		t.Fatalf("delete after disassociate: %v", err)
	}
}

func TestDeleteReferencedItemBlocked(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ips, _ := m.CreateIPSet(ctx, driver.CreateIPSetInput{
		Name: "refips", Scope: driver.ScopeRegional, IPAddressVersion: "IPV4",
	})

	// A web ACL whose rule references the IP set by ARN.
	rules := json.RawMessage(`[{"Name":"r","Statement":{"IPSetReferenceStatement":{"ARN":"` + ips.ARN + `"}}}]`)
	acl, _ := m.CreateWebACL(ctx, driver.CreateWebACLInput{Name: "refacl", Scope: driver.ScopeRegional, Rules: rules})

	if err := m.DeleteIPSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: ips.ID}, ips.LockToken); !isAssociatedItem(err) {
		t.Fatalf("delete referenced IP set: want WAFAssociatedItemException, got %v", err)
	}

	// Removing the reference (delete the web ACL) frees the IP set for deletion.
	if err := m.DeleteWebACL(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: acl.ID}, acl.LockToken); err != nil {
		t.Fatalf("DeleteWebACL: %v", err)
	}

	if err := m.DeleteIPSet(ctx, driver.Ref{Scope: driver.ScopeRegional, ID: ips.ID}, ips.LockToken); err != nil {
		t.Fatalf("delete IP set after reference removed: %v", err)
	}
}
