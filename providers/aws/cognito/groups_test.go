package cognito

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func int32Ptr(v int32) *int32 { return &v }

func TestGroupLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "groups")

	g, err := m.CreateGroup(ctx, driver.CreateGroupInput{
		UserPoolID: pool.ID, GroupName: "admins", Description: "Admins",
		RoleARN: "arn:aws:iam::123456789012:role/admin", Precedence: int32Ptr(1),
	})
	requireNoError(t, err, "CreateGroup")

	if g.GroupName != "admins" || g.UserPoolID != pool.ID || *g.Precedence != 1 || g.CreationDate.IsZero() {
		t.Fatalf("group = %+v", g)
	}

	_, err = m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "admins"})
	assertException(t, err, driver.ExGroupExists, "A group with the name admins already exists.")

	_, err = m.GetGroup(ctx, pool.ID, "nope")
	assertException(t, err, driver.ExResourceNotFound, "Group not found.")

	desc := "Administrators"
	up, err := m.UpdateGroup(ctx, driver.UpdateGroupInput{UserPoolID: pool.ID, GroupName: "admins", Description: &desc})
	requireNoError(t, err, "UpdateGroup")

	if up.Description != desc || up.RoleARN == "" || *up.Precedence != 1 {
		t.Fatalf("updated group = %+v, want description changed and the rest kept", up)
	}

	_, err = m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "readers"})
	requireNoError(t, err, "CreateGroup readers")

	groups, next, err := m.ListGroups(ctx, pool.ID, driver.Pagination{MaxResults: 1})
	requireNoError(t, err, "ListGroups")

	if len(groups) != 1 || groups[0].GroupName != "admins" || next == "" {
		t.Fatalf("page 1 = %+v next=%q", groups, next)
	}

	groups, next, err = m.ListGroups(ctx, pool.ID, driver.Pagination{MaxResults: 1, NextToken: next})
	requireNoError(t, err, "ListGroups page 2")

	if len(groups) != 1 || groups[0].GroupName != "readers" || next != "" {
		t.Fatalf("page 2 = %+v next=%q", groups, next)
	}

	requireNoError(t, m.DeleteGroup(ctx, pool.ID, "readers"), "DeleteGroup")
	assertException(t, m.DeleteGroup(ctx, pool.ID, "readers"), driver.ExResourceNotFound, "Group not found.")

	_, err = m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: "us-east-1_missing00", GroupName: "x"})
	assertException(t, err, driver.ExResourceNotFound, "")
}

func TestGroupMembership(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "members")
	mustCreateUser(t, m, pool.ID, "alice")
	mustCreateUser(t, m, pool.ID, "bob")

	for _, name := range []string{"admins", "readers"} {
		_, err := m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: name})
		requireNoError(t, err, "CreateGroup "+name)
	}

	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "admins"), "add alice admins")
	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "admins"), "re-add is a no-op")
	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "readers"), "add alice readers")
	requireNoError(t, m.AdminAddUserToGroup(ctx, pool.ID, "bob", "readers"), "add bob readers")

	assertException(t, m.AdminAddUserToGroup(ctx, pool.ID, "carol", "admins"), driver.ExUserNotFound, "User does not exist.")
	assertException(t, m.AdminAddUserToGroup(ctx, pool.ID, "alice", "ghosts"), driver.ExResourceNotFound, "Group not found.")

	groups, _, err := m.AdminListGroupsForUser(ctx, pool.ID, "alice", driver.Pagination{})
	requireNoError(t, err, "AdminListGroupsForUser")

	if len(groups) != 2 {
		t.Fatalf("alice groups = %+v", groups)
	}

	users, _, err := m.ListUsersInGroup(ctx, pool.ID, "readers", driver.Pagination{})
	requireNoError(t, err, "ListUsersInGroup")

	if len(users) != 2 || users[0].Username != "alice" || users[1].Username != "bob" {
		t.Fatalf("readers = %+v", users)
	}

	requireNoError(t, m.AdminRemoveUserFromGroup(ctx, pool.ID, "alice", "readers"), "remove")

	users, _, _ = m.ListUsersInGroup(ctx, pool.ID, "readers", driver.Pagination{})
	if len(users) != 1 || users[0].Username != "bob" {
		t.Fatalf("readers after remove = %+v", users)
	}

	requireNoError(t, m.DeleteGroup(ctx, pool.ID, "admins"), "DeleteGroup admins")

	groups, _, _ = m.AdminListGroupsForUser(ctx, pool.ID, "alice", driver.Pagination{})
	if len(groups) != 0 {
		t.Fatalf("alice groups after group delete = %+v", groups)
	}

	_, err = m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "admins"})
	requireNoError(t, err, "recreate admins")

	groups, _, _ = m.AdminListGroupsForUser(ctx, pool.ID, "alice", driver.Pagination{})
	if len(groups) != 0 {
		t.Fatalf("recreated group must start empty, alice has %+v", groups)
	}

	_, _, err = m.ListUsersInGroup(ctx, pool.ID, "ghosts", driver.Pagination{})
	assertException(t, err, driver.ExResourceNotFound, "Group not found.")
}

func TestDeleteUserPoolCascadesGroups(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "cascade")

	_, err := m.CreateGroup(ctx, driver.CreateGroupInput{UserPoolID: pool.ID, GroupName: "g"})
	requireNoError(t, err, "CreateGroup")
	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool")

	if n := len(m.groups.Keys()); n != 0 {
		t.Fatalf("%d groups left after pool delete", n)
	}
}
