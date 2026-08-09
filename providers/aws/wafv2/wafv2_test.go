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

func isOptimisticLock(err error) bool {
	var apiErr *driver.APIError

	return stderrors.As(err, &apiErr) && apiErr.Exception == driver.ExOptimisticLock
}
