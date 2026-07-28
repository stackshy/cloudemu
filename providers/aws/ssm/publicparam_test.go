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

// Only the ami-id family is synthesized. Other public parameters carry
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

// AWS answers ParameterNotFound for a path it does not publish. Resolving
// anything that ends in /ami-id would accept typos and invented distros, and
// the caller would launch from an image that exists nowhere but here.
func TestUnpublishedAMIPathIsNotFound(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	for _, name := range []string{
		"/aws/service/canonicl/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
		"/aws/service/totally-invented/distro/ami-id",
	} {
		if _, err := m.GetParameter(ctx, name, false); err == nil {
			t.Errorf("unpublished path %q should be NotFound", name)
		}
	}
}

// The published trees callers actually read from all resolve.
func TestPublishedAMITreesResolve(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	for _, name := range []string{
		ubuntu2204AMIParam,
		"/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64/ami-id",
		"/aws/service/ami-windows-latest/Windows_Server-2022-English-Full-Base/ami-id",
		"/aws/service/bottlerocket/aws-k8s-1.29/x86_64/latest/ami-id",
		"/aws/service/eks/optimized-ami/1.29/amazon-linux-2/recommended/ami-id",
	} {
		p, err := m.GetParameter(ctx, name, false)
		if err != nil {
			t.Errorf("published path %q should resolve: %v", name, err)
			continue
		}

		if !strings.HasPrefix(p.Value, "ami-") {
			t.Errorf("%q resolved to %q, want an ami- id", name, p.Value)
		}
	}
}
