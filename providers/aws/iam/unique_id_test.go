package iam

import (
	"context"
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// uniqueIDShape is the shape of an IAM unique id: a 4-letter prefix and 17
// uppercase base32 characters.
var uniqueIDShape = regexp.MustCompile(`^(AIDA|AROA|AGPA|AIPA)[A-Z2-7]{17}$`)

// TestUniqueIDsAreRandom checks users, roles, groups and instance profiles
// get 21-character random unique ids.
func TestUniqueIDsAreRandom(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	u, err := m.CreateUser(ctx, driver.UserConfig{Name: "u"})
	requireNoError(t, err)
	r, err := m.CreateRole(ctx, driver.RoleConfig{Name: "r", AssumeRolePolicyDoc: "{}"})
	requireNoError(t, err)
	g, err := m.CreateGroup(ctx, driver.GroupConfig{Name: "g"})
	requireNoError(t, err)
	ip, err := m.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "ip"})
	requireNoError(t, err)

	for _, id := range []string{u.ID, r.ID, g.ID, ip.ID} {
		if !uniqueIDShape.MatchString(id) {
			t.Errorf("unique id %q does not have the IAM shape", id)
		}
	}
}

// TestRecreatedUserAfterRestoreIsNotTrusted models a restart: state is
// restored into a fresh process (the id counter starts over), the trusted
// user is deleted and created again. The new user gets a new unique id, so
// the trust saved for the old one does not match it.
func TestRecreatedUserAfterRestoreIsNotTrusted(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	old, err := src.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	requireNoError(t, err)

	_, err = src.CreateRole(ctx, driver.RoleConfig{
		Name: "target", AssumeRolePolicyDoc: trustDoc(allowStmt(`{"AWS":"` + trustUserARN + `"}`)),
	})
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	idgen.Reset()

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	req := &driver.TrustRequest{RoleName: "target", Action: "sts:AssumeRole", CallerARNs: []string{trustUserARN}, CallerAccount: trustAcct}
	assertEqual(t, true, dst.EvaluateTrust(ctx, req).Allow)

	requireNoError(t, dst.DeleteUser(ctx, "alice"))

	again, err := dst.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	requireNoError(t, err)

	if again.ID == old.ID {
		t.Fatalf("recreated user reused the unique id %s", old.ID)
	}

	assertEqual(t, false, dst.EvaluateTrust(ctx, req).Allow)
}
