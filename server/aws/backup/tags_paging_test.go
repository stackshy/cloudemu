package backup_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	backupapi "github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func TestListTagsPagingOverWire(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	v := mustCreateVault(t, c, "paged-vault")
	arn := aws.ToString(v.BackupVaultArn)

	if _, err := c.TagResource(ctx, &backupapi.TagResourceInput{
		ResourceArn: aws.String(arn), Tags: map[string]string{"a": "1", "b": "2"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	seen := map[string]string{}
	in := &backupapi.ListTagsInput{ResourceArn: aws.String(arn), MaxResults: aws.Int32(1)}

	for pages := 0; ; pages++ {
		out, err := c.ListTags(ctx, in)
		if err != nil {
			t.Fatalf("ListTags page %d: %v", pages, err)
		}

		if len(out.Tags) != 1 {
			t.Fatalf("page %d: want 1 tag, got %v", pages, out.Tags)
		}

		for k, val := range out.Tags {
			seen[k] = val
		}

		if out.NextToken == nil {
			break
		}

		in.NextToken = out.NextToken
	}

	// The vault was created with env=test, so three tags in total.
	if len(seen) != 3 || seen["env"] != "test" || seen["a"] != "1" || seen["b"] != "2" {
		t.Fatalf("paging lost tags: %v", seen)
	}

	_, err := c.ListTags(ctx, &backupapi.ListTagsInput{ResourceArn: aws.String(arn), MaxResults: aws.Int32(1001)})

	var ipv *backuptypes.InvalidParameterValueException
	if !errors.As(err, &ipv) {
		t.Fatalf("maxResults 1001: want InvalidParameterValueException, got %v", err)
	}
}

func TestListTagsBadMaxResultsRaw(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Backup: cloud.Backup}))
	t.Cleanup(ts.Close)

	arn := "arn:aws:backup:us-east-1:123456789012:backup-vault:nope"

	for _, q := range []string{"0", "abc"} {
		u := ts.URL + "/tags/" + url.PathEscape(arn) + "/?maxResults=" + q

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("X-Amzn-Errortype") != "InvalidParameterValueException" {
			t.Fatalf("maxResults=%s: status %d type %q", q, resp.StatusCode, resp.Header.Get("X-Amzn-Errortype"))
		}
	}
}

func TestListBadNextTokenOverWire(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	v := mustCreateVault(t, c, "token-vault")

	var ipv *backuptypes.InvalidParameterValueException

	_, err := c.ListTags(ctx, &backupapi.ListTagsInput{ResourceArn: v.BackupVaultArn, NextToken: aws.String("garbage")})
	if !errors.As(err, &ipv) {
		t.Fatalf("ListTags bad token: want InvalidParameterValueException, got %v", err)
	}

	_, err = c.ListBackupVaults(ctx, &backupapi.ListBackupVaultsInput{NextToken: aws.String("garbage")})
	if !errors.As(err, &ipv) {
		t.Fatalf("ListBackupVaults bad token: want InvalidParameterValueException, got %v", err)
	}
}

func TestPutVaultNotificationsEmptyEventsEchoed(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Backup: cloud.Backup}))
	t.Cleanup(ts.Close)

	do := func(method, path, body string) (int, string) {
		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(b)
	}

	if st, b := do(http.MethodPut, "/backup-vaults/empty-v", "{}"); st != http.StatusOK {
		t.Fatalf("create vault: %d %s", st, b)
	}

	put := `{"SNSTopicArn":"arn:aws:sns:us-east-1:123456789012:t","BackupVaultEvents":[]}`
	if st, b := do(http.MethodPut, "/backup-vaults/empty-v/notification-configuration", put); st != http.StatusOK {
		t.Fatalf("put empty events: %d %s", st, b)
	}

	st, b := do(http.MethodGet, "/backup-vaults/empty-v/notification-configuration", "")
	if st != http.StatusOK || !strings.Contains(b, `"BackupVaultEvents":[]`) {
		t.Fatalf("get must echo []: %d %s", st, b)
	}
}

func TestPutVaultNotificationsBadEventOverWire(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	mustCreateVault(t, c, "notify-vault")

	_, err := c.PutBackupVaultNotifications(ctx, &backupapi.PutBackupVaultNotificationsInput{
		BackupVaultName:   aws.String("notify-vault"),
		SNSTopicArn:       aws.String("arn:aws:sns:us-east-1:123456789012:t"),
		BackupVaultEvents: []backuptypes.BackupVaultEvent{"BOGUS_EVENT"},
	})

	var ipv *backuptypes.InvalidParameterValueException
	if !errors.As(err, &ipv) {
		t.Fatalf("want InvalidParameterValueException, got %v", err)
	}

	var rnf *backuptypes.ResourceNotFoundException
	if _, gerr := c.GetBackupVaultNotifications(ctx, &backupapi.GetBackupVaultNotificationsInput{
		BackupVaultName: aws.String("notify-vault"),
	}); !errors.As(gerr, &rnf) {
		t.Fatalf("rejected put must not store a config, got %v", gerr)
	}
}
