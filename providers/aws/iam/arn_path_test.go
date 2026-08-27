package iam

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const nestedPath = "/div/sub/"

// TestCreateEntityARNEmbedsPath verifies that a non-default Path is folded into
// the entity ARN (arn:aws:iam::123456789012:user/div/sub/bob), while the default
// path "/" leaves the ARN unchanged (regression guard).
func TestCreateEntityARNEmbedsPath(t *testing.T) {
	ctx := context.Background()

	makeUser := func(m *Mock, path string) (string, error) {
		info, err := m.CreateUser(ctx, driver.UserConfig{Name: "bob", Path: path})
		if err != nil {
			return "", err
		}
		return info.ARN, nil
	}
	makeRole := func(m *Mock, path string) (string, error) {
		info, err := m.CreateRole(ctx, driver.RoleConfig{Name: "r1", Path: path})
		if err != nil {
			return "", err
		}
		return info.ARN, nil
	}
	makePolicy := func(m *Mock, path string) (string, error) {
		info, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p1", Path: path, PolicyDocument: makePolicyDoc(nil)})
		if err != nil {
			return "", err
		}
		return info.ARN, nil
	}
	makeGroup := func(m *Mock, path string) (string, error) {
		info, err := m.CreateGroup(ctx, driver.GroupConfig{Name: "g1", Path: path})
		if err != nil {
			return "", err
		}
		return info.ARN, nil
	}
	makeProfile := func(m *Mock, path string) (string, error) {
		info, err := m.CreateInstanceProfile(ctx, driver.InstanceProfileConfig{Name: "ip1", Path: path})
		if err != nil {
			return "", err
		}
		return info.ARN, nil
	}

	tests := []struct {
		name        string
		make        func(*Mock, string) (string, error)
		wantNested  string
		wantDefault string
	}{
		{"user", makeUser, "arn:aws:iam::123456789012:user/div/sub/bob", "arn:aws:iam::123456789012:user/bob"},
		{"role", makeRole, "arn:aws:iam::123456789012:role/div/sub/r1", "arn:aws:iam::123456789012:role/r1"},
		{"policy", makePolicy, "arn:aws:iam::123456789012:policy/div/sub/p1", "arn:aws:iam::123456789012:policy/p1"},
		{"group", makeGroup, "arn:aws:iam::123456789012:group/div/sub/g1", "arn:aws:iam::123456789012:group/g1"},
		{
			"instance-profile", makeProfile,
			"arn:aws:iam::123456789012:instance-profile/div/sub/ip1",
			"arn:aws:iam::123456789012:instance-profile/ip1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+" nested path", func(t *testing.T) {
			arn, err := tc.make(newTestMock(), nestedPath)
			requireNoError(t, err)
			assertEqual(t, tc.wantNested, arn)
		})

		t.Run(tc.name+" default path unchanged", func(t *testing.T) {
			arn, err := tc.make(newTestMock(), "")
			requireNoError(t, err)
			assertEqual(t, tc.wantDefault, arn)
		})
	}
}

// TestConcurrentTagAndGet exercises the tag-map read/write paths concurrently so
// that `go test -race` flags any unsynchronized access to an entity's Tags map.
func TestConcurrentTagAndGet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "alice"})
	requireNoError(t, err)
	_, err = m.CreateRole(ctx, driver.RoleConfig{Name: "svc"})
	requireNoError(t, err)

	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = m.TagUser(ctx, "alice", map[string]string{"team": "dev"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_, _ = m.GetUser(ctx, "alice")
			_, _ = m.ListUsers(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = m.TagRole(ctx, "svc", map[string]string{"env": "test"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_, _ = m.GetRole(ctx, "svc")
			_, _ = m.ListRoles(ctx)
		}
	}()

	wg.Wait()
}
