package wafv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// defaultWebACLCapacity is the notional WCU capacity reported for an emulated
// web ACL; the emulator does not compute real rule capacity.
const defaultWebACLCapacity = 10

func copyWebACL(a *driver.WebACL) driver.WebACL {
	out := *a
	out.Tags = copyTags(a.Tags)
	out.DefaultAction = copyBytes(a.DefaultAction)
	out.VisibilityConfig = copyBytes(a.VisibilityConfig)
	out.Rules = copyBytes(a.Rules)
	out.TokenDomains = copyBytes(a.TokenDomains)
	out.CustomResponses = copyBytes(a.CustomResponses)
	out.CaptchaConfig = copyBytes(a.CaptchaConfig)
	out.ChallengeConfig = copyBytes(a.ChallengeConfig)

	return out
}

func (m *Mock) webACLByName(scope, name string) (*webACLData, bool) {
	for _, wd := range m.webACLs.All() {
		wd.mu.RLock()
		match := wd.acl.Scope == scope && wd.acl.Name == name
		wd.mu.RUnlock()

		if match {
			return wd, true
		}
	}

	return nil, false
}

// CreateWebACL creates a web ACL, storing its rule configuration verbatim.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateWebACL(_ context.Context, in driver.CreateWebACLInput) (*driver.WebACL, error) {
	if in.Name == "" || in.Scope == "" {
		return nil, invalidParameter("Name and Scope are required")
	}

	if _, exists := m.webACLByName(in.Scope, in.Name); exists {
		return nil, duplicate("web ACL %q already exists in scope %s", in.Name, in.Scope)
	}

	id := idgen.GenerateID("")
	acl := driver.WebACL{
		ID:               id,
		Name:             in.Name,
		ARN:              m.arn(in.Scope, "webacl", in.Name, id),
		Scope:            in.Scope,
		Description:      in.Description,
		LockToken:        newLockToken(),
		Capacity:         defaultWebACLCapacity,
		LabelNamespace:   "awswaf:" + m.opts.AccountID + ":webacl:" + in.Name + ":",
		DefaultAction:    in.DefaultAction,
		VisibilityConfig: in.VisibilityConfig,
		Rules:            in.Rules,
		TokenDomains:     in.TokenDomains,
		CustomResponses:  in.CustomResponses,
		CaptchaConfig:    in.CaptchaConfig,
		ChallengeConfig:  in.ChallengeConfig,
		Tags:             copyTags(in.Tags),
	}

	m.webACLs.Set(key(in.Scope, id), &webACLData{acl: acl})

	out := copyWebACL(&acl)

	return &out, nil
}

func (m *Mock) getWebACLData(ref driver.Ref) (*webACLData, error) {
	wd, ok := m.webACLs.Get(key(ref.Scope, ref.ID))
	if !ok {
		return nil, nonexistent("web ACL %q not found in scope %s", ref.ID, ref.Scope)
	}

	return wd, nil
}

// GetWebACL returns a web ACL by (scope,id).
func (m *Mock) GetWebACL(_ context.Context, ref driver.Ref) (*driver.WebACL, error) {
	wd, err := m.getWebACLData(ref)
	if err != nil {
		return nil, err
	}

	wd.mu.RLock()
	defer wd.mu.RUnlock()

	out := copyWebACL(&wd.acl)

	return &out, nil
}

// UpdateWebACL replaces a web ACL's mutable fields, enforcing the lock token and
// rotating it on success.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) UpdateWebACL(_ context.Context, in driver.UpdateWebACLInput) (string, error) {
	wd, err := m.getWebACLData(driver.Ref{Scope: in.Scope, ID: in.ID})
	if err != nil {
		return "", err
	}

	wd.mu.Lock()
	defer wd.mu.Unlock()

	if wd.acl.LockToken != in.LockToken {
		return "", staleLock("stale lock token for web ACL %q", in.ID)
	}

	wd.acl.Description = in.Description
	wd.acl.DefaultAction = in.DefaultAction
	wd.acl.VisibilityConfig = in.VisibilityConfig
	wd.acl.Rules = in.Rules
	wd.acl.TokenDomains = in.TokenDomains
	wd.acl.CustomResponses = in.CustomResponses
	wd.acl.CaptchaConfig = in.CaptchaConfig
	wd.acl.ChallengeConfig = in.ChallengeConfig
	wd.acl.LockToken = newLockToken()

	return wd.acl.LockToken, nil
}

// DeleteWebACL removes a web ACL, enforcing the lock token.
func (m *Mock) DeleteWebACL(_ context.Context, ref driver.Ref, lockToken string) error {
	wd, err := m.getWebACLData(ref)
	if err != nil {
		return err
	}

	wd.mu.Lock()
	if wd.acl.LockToken != lockToken {
		wd.mu.Unlock()

		return staleLock("stale lock token for web ACL %q", ref.ID)
	}
	wd.mu.Unlock()

	m.webACLs.Delete(key(ref.Scope, ref.ID))

	return nil
}

// ListWebACLs returns all web ACLs in a scope.
func (m *Mock) ListWebACLs(_ context.Context, scope string) ([]driver.WebACL, error) {
	all := m.webACLs.All()
	out := make([]driver.WebACL, 0, len(all))

	for _, wd := range all {
		wd.mu.RLock()
		if wd.acl.Scope == scope {
			out = append(out, copyWebACL(&wd.acl))
		}
		wd.mu.RUnlock()
	}

	return out, nil
}
