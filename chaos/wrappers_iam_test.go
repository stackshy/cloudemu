package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
)

func newChaosIAM(t *testing.T) (iamdriver.IAM, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapIAM(cloudemu.NewAWS().IAM, e), e
}

func TestWrapIAMCreateUserChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	if _, err := i.CreateUser(ctx, iamdriver.UserConfig{Name: "u"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.CreateUser(ctx, iamdriver.UserConfig{Name: "u2"}); err == nil {
		t.Error("expected chaos error on CreateUser")
	}
}

func TestWrapIAMDeleteUserChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()
	_, _ = i.CreateUser(ctx, iamdriver.UserConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if err := i.DeleteUser(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteUser")
	}
}

func TestWrapIAMGetUserChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()
	_, _ = i.CreateUser(ctx, iamdriver.UserConfig{Name: "g"})

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.GetUser(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetUser")
	}
}

func TestWrapIAMListUsersChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.ListUsers(ctx); err == nil {
		t.Error("expected chaos error on ListUsers")
	}
}

func TestWrapIAMCreateRoleChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.CreateRole(ctx, iamdriver.RoleConfig{Name: "r"}); err == nil {
		t.Error("expected chaos error on CreateRole")
	}
}

func TestWrapIAMDeleteRoleChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()
	_, _ = i.CreateRole(ctx, iamdriver.RoleConfig{Name: "delrole"})

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if err := i.DeleteRole(ctx, "delrole"); err == nil {
		t.Error("expected chaos error on DeleteRole")
	}
}

func TestWrapIAMGetRoleChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()
	_, _ = i.CreateRole(ctx, iamdriver.RoleConfig{Name: "gr"})

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.GetRole(ctx, "gr"); err == nil {
		t.Error("expected chaos error on GetRole")
	}
}

func TestWrapIAMListRolesChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.ListRoles(ctx); err == nil {
		t.Error("expected chaos error on ListRoles")
	}
}

func TestWrapIAMCreatePolicyChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "p", PolicyDocument: "{}"}); err == nil {
		t.Error("expected chaos error on CreatePolicy")
	}
}

func TestWrapIAMDeletePolicyChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if err := i.DeletePolicy(ctx, "arn:aws:iam::x:policy/p"); err == nil {
		t.Error("expected chaos error on DeletePolicy")
	}
}

func TestWrapIAMGetPolicyChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.GetPolicy(ctx, "arn:aws:iam::x:policy/p"); err == nil {
		t.Error("expected chaos error on GetPolicy")
	}
}

func TestWrapIAMListPoliciesChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.ListPolicies(ctx); err == nil {
		t.Error("expected chaos error on ListPolicies")
	}
}

func TestWrapIAMCheckPermissionChaos(t *testing.T) {
	i, e := newChaosIAM(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("iam", time.Hour))

	if _, err := i.CheckPermission(ctx, "u", "s3:GetObject", "*"); err == nil {
		t.Error("expected chaos error on CheckPermission")
	}
}
