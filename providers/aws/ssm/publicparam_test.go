package ssm

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
)

const ubuntu2204AMIParam = "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"

// Callers resolve the current distro image through this parameter rather than
// pinning an AMI id that goes stale. AWS publishes it in every account, so a
// NotFound here stops any flow that launches an instance.
func TestGetPublicAMIParameterWithoutPut(t *testing.T) {
	m := New(config.NewOptions())

	p, err := m.GetParameter(context.Background(), ubuntu2204AMIParam, false)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if !strings.HasPrefix(p.Value, "ami-") {
		t.Errorf("value = %q, want an ami- id", p.Value)
	}
}

// Repeated reads must resolve to the same image; a caller that re-reads while
// reconciling would otherwise see the AMI change under it.
func TestPublicAMIParameterIsStable(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	first, err := m.GetParameter(ctx, ubuntu2204AMIParam, false)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}

	second, err := m.GetParameter(ctx, ubuntu2204AMIParam, false)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}

	if first.Value != second.Value {
		t.Errorf("unstable: %q then %q", first.Value, second.Value)
	}
}

// Two distros must not collide on one image id.
func TestDifferentAMIParametersDiffer(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	a, err := m.GetParameter(ctx, ubuntu2204AMIParam, false)
	if err != nil {
		t.Fatalf("ubuntu: %v", err)
	}

	b, err := m.GetParameter(ctx,
		"/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp2/ami-id", false)
	if err != nil {
		t.Fatalf("ubuntu 24.04: %v", err)
	}

	if a.Value == b.Value {
		t.Errorf("distinct parameters resolved to the same AMI %q", a.Value)
	}
}

// Only the ami-id family is synthesised. Other public parameters carry
// payloads that cannot be derived, and returning an invented value would be
// worse than saying it is not there.
func TestNonAMIPublicParameterStillNotFound(t *testing.T) {
	m := New(config.NewOptions())

	_, err := m.GetParameter(context.Background(),
		"/aws/service/ecs/optimized-ami/amazon-linux-2/recommended", false)
	if err == nil {
		t.Error("non-ami public parameter should still be NotFound")
	}
}

// A user parameter that was never put stays NotFound — there the error is a
// real caller bug worth surfacing.
func TestUserParameterStillNotFound(t *testing.T) {
	m := New(config.NewOptions())

	if _, err := m.GetParameter(context.Background(), "/my/app/config", false); err == nil {
		t.Error("unset user parameter should be NotFound")
	}
}

func TestGetParametersResolvesPublicAMI(t *testing.T) {
	m := New(config.NewOptions())

	found, invalid, err := m.GetParameters(context.Background(),
		[]string{ubuntu2204AMIParam, "/not/published"}, false)
	if err != nil {
		t.Fatalf("GetParameters: %v", err)
	}

	if len(found) != 1 || !strings.HasPrefix(found[0].Value, "ami-") {
		t.Errorf("found = %+v, want one ami- value", found)
	}

	if len(invalid) != 1 || invalid[0] != "/not/published" {
		t.Errorf("invalid = %v, want [/not/published]", invalid)
	}
}
