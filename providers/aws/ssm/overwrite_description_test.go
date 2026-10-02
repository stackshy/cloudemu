package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func describedDescription(t *testing.T, m *ssm.Mock, name string) string {
	t.Helper()

	metas, err := m.DescribeParameters(context.Background())
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	for _, md := range metas {
		if md.Name == name {
			return md.Description
		}
	}

	t.Fatalf("parameter %q not described", name)

	return ""
}

func TestOverwriteDescriptionSet(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/d", Value: "1", Type: driver.TypeString, Description: "kept",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	steps := []struct {
		cfg  driver.PutConfig
		want string
	}{
		{cfg: driver.PutConfig{Value: "2"}, want: "kept"},
		{cfg: driver.PutConfig{Value: "3", Description: "new"}, want: "new"},
		{cfg: driver.PutConfig{Value: "4", DescriptionSet: true}, want: ""},
	}

	for i, s := range steps {
		s.cfg.Name, s.cfg.Overwrite = "/d", true

		if _, _, err := m.PutParameter(ctx, s.cfg); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		if got := describedDescription(t, m, "/d"); got != s.want {
			t.Errorf("step %d: Description = %q, want %q", i, got, s.want)
		}
	}
}

func TestLabelMissingVersionIsVersionNotFound(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{Name: "/l", Value: "1", Type: driver.TypeString}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, err := m.LabelParameterVersion(ctx, "/l", 7, []string{"prod"}); !errors.Is(err, driver.ErrVersionNotFound) {
		t.Fatalf("LabelParameterVersion(missing version): err = %v, want ErrVersionNotFound", err)
	}
}
