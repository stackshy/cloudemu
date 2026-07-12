package ssm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func newMock() *ssm.Mock {
	return ssm.New(config.NewOptions())
}

func TestPutOverwriteAndHistory(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	v, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "1", Type: driver.TypeString})
	if err != nil {
		t.Fatalf("PutParameter v1: %v", err)
	}

	if v != 1 {
		t.Fatalf("first version = %d, want 1", v)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "2", Type: driver.TypeString}); err == nil {
		t.Fatal("PutParameter without Overwrite: want AlreadyExists, got nil")
	} else if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}

	v2, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/a", Value: "2", Type: driver.TypeString, Overwrite: true})
	if err != nil {
		t.Fatalf("PutParameter overwrite: %v", err)
	}

	if v2 != 2 {
		t.Fatalf("overwrite version = %d, want 2", v2)
	}

	hist, err := m.GetParameterHistory(ctx, "/a")
	if err != nil {
		t.Fatalf("GetParameterHistory: %v", err)
	}

	if len(hist) != 2 || hist[0].Value != "1" || hist[1].Value != "2" {
		t.Fatalf("history = %+v, want [1 2]", hist)
	}
}

func TestLabelParameterVersionAndSelector(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/svc/cfg", Value: "old", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/svc/cfg", Value: "new", Type: driver.TypeString, Overwrite: true}); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	// Label v1 "prod".
	applied, invalid, err := m.LabelParameterVersion(ctx, "/svc/cfg", 1, []string{"prod"})
	if err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	if applied != 1 || len(invalid) != 0 {
		t.Fatalf("applied=%d invalid=%v, want 1 []", applied, invalid)
	}

	got, err := m.GetParameter(ctx, "/svc/cfg:prod", false)
	if err != nil {
		t.Fatalf("GetParameter(:prod): %v", err)
	}

	if got.Value != "old" || got.Version != 1 {
		t.Fatalf("label selector got %q v%d, want old v1", got.Value, got.Version)
	}

	// Latest (no selector) is still v2.
	latest, err := m.GetParameter(ctx, "/svc/cfg", false)
	if err != nil {
		t.Fatalf("GetParameter(latest): %v", err)
	}

	if latest.Value != "new" || latest.Version != 2 {
		t.Fatalf("latest got %q v%d, want new v2", latest.Value, latest.Version)
	}
}

func TestGetParametersByPathRecursive(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"/p/a", "/p/b", "/p/sub/c"} {
		if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: n, Value: "x", Type: driver.TypeString}); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}

	shallow, err := m.GetParametersByPath(ctx, driver.GetByPathInput{Path: "/p"})
	if err != nil {
		t.Fatalf("GetParametersByPath: %v", err)
	}

	if len(shallow) != 2 {
		t.Fatalf("non-recursive returned %d, want 2", len(shallow))
	}

	deep, err := m.GetParametersByPath(ctx, driver.GetByPathInput{Path: "/p", Recursive: true})
	if err != nil {
		t.Fatalf("GetParametersByPath recursive: %v", err)
	}

	if len(deep) != 3 {
		t.Fatalf("recursive returned %d, want 3", len(deep))
	}
}

func TestDeleteParameters(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/d/1", Value: "x", Type: driver.TypeString}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	deleted, invalid, err := m.DeleteParameters(ctx, []string{"/d/1", "/d/missing"})
	if err != nil {
		t.Fatalf("DeleteParameters: %v", err)
	}

	if len(deleted) != 1 || deleted[0] != "/d/1" {
		t.Fatalf("deleted = %v, want [/d/1]", deleted)
	}

	if len(invalid) != 1 || invalid[0] != "/d/missing" {
		t.Fatalf("invalid = %v, want [/d/missing]", invalid)
	}
}
