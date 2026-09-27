package backup_test

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

func requireException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestListTagsPaging(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	v := mustVault(t, m, "v1")

	requireNoError(t, m.TagResource(ctx, v.Arn, map[string]string{"c": "3", "a": "1", "b": "2"}))

	seen := map[string]string{}
	page := driver.Page{MaxResults: 1}

	for i := 0; ; i++ {
		tags, next, err := m.ListTags(ctx, v.Arn, page)
		requireNoError(t, err)

		if len(tags) != 1 {
			t.Fatalf("page %d: want 1 tag, got %v", i, tags)
		}

		for k, val := range tags {
			seen[k] = val
		}

		if next == "" {
			break
		}

		page.NextToken = next
	}

	if len(seen) != 3 || seen["a"] != "1" || seen["b"] != "2" || seen["c"] != "3" {
		t.Fatalf("paging lost tags: %v", seen)
	}

	all, next, err := m.ListTags(ctx, v.Arn, driver.Page{})
	requireNoError(t, err)

	if len(all) != 3 || next != "" {
		t.Fatalf("default page: tags=%v next=%q", all, next)
	}
}

func TestListTagsMaxResultsBounds(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	v := mustVault(t, m, "v1")

	for _, n := range []int32{-1, 1001} {
		_, _, err := m.ListTags(ctx, v.Arn, driver.Page{MaxResults: n})
		requireException(t, err, driver.ExInvalidParameter)
	}

	if _, _, err := m.ListTags(ctx, v.Arn, driver.Page{MaxResults: 1000}); err != nil {
		t.Fatalf("maxResults 1000 must be accepted: %v", err)
	}
}

func TestListBadNextTokenRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	v := mustVault(t, m, "v1")

	requireNoError(t, m.TagResource(ctx, v.Arn, map[string]string{"a": "1"}))

	for _, tok := range []string{"garbage", "-1"} {
		_, _, err := m.ListTags(ctx, v.Arn, driver.Page{NextToken: tok})
		requireException(t, err, driver.ExInvalidParameter)

		_, _, err = m.ListBackupVaults(ctx, driver.Page{NextToken: tok})
		requireException(t, err, driver.ExInvalidParameter)
	}
}

func TestPutVaultNotificationsEmptyEvents(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	mustVault(t, m, "v1")

	requireNoError(t, m.PutBackupVaultNotifications(ctx, &driver.PutVaultNotificationsInput{
		Name: "v1", SNSTopicArn: "arn:aws:sns:us-east-1:123456789012:t", BackupVaultEvents: []string{},
	}))

	_, n, err := m.GetBackupVaultNotifications(ctx, "v1")
	requireNoError(t, err)

	if n.BackupVaultEvents == nil || len(n.BackupVaultEvents) != 0 {
		t.Fatalf("want an empty non-nil list, got %#v", n.BackupVaultEvents)
	}
}

func TestPutVaultNotificationsEventValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	mustVault(t, m, "v1")

	const topic = "arn:aws:sns:us-east-1:123456789012:t"

	err := m.PutBackupVaultNotifications(ctx, &driver.PutVaultNotificationsInput{
		Name: "v1", SNSTopicArn: topic, BackupVaultEvents: []string{"BACKUP_JOB_COMPLETED", "NOT_AN_EVENT"},
	})
	requireException(t, err, driver.ExInvalidParameter)

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument code, got %v", err)
	}

	// A rejected put must not leave a partial config behind.
	if _, _, gerr := m.GetBackupVaultNotifications(ctx, "v1"); !cerrors.IsNotFound(gerr) {
		t.Fatalf("rejected put stored a config: %v", gerr)
	}

	err = m.PutBackupVaultNotifications(ctx, &driver.PutVaultNotificationsInput{Name: "v1", SNSTopicArn: topic})
	requireException(t, err, driver.ExMissingParameter)

	events := []string{"BACKUP_JOB_STARTED", "RESTORE_JOB_COMPLETED", "COPY_JOB_FAILED", "RECOVERY_POINT_MODIFIED"}
	requireNoError(t, m.PutBackupVaultNotifications(ctx, &driver.PutVaultNotificationsInput{
		Name: "v1", SNSTopicArn: topic, BackupVaultEvents: events,
	}))

	_, n, err := m.GetBackupVaultNotifications(ctx, "v1")
	requireNoError(t, err)

	if len(n.BackupVaultEvents) != len(events) || n.BackupVaultEvents[0] != events[0] {
		t.Fatalf("events not stored: %v", n.BackupVaultEvents)
	}
}
