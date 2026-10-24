package rds_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKRDSDescribeEventsWindowAndPaging covers DescribeEvents over the wire:
// lifecycle events are recorded, StartTime/EndTime and the filters bound the
// result, and MaxRecords/Marker page through the oldest-first list.
func TestSDKRDSDescribeEventsWindowAndPaging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const n = 25

	for i := range n {
		if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String(fmt.Sprintf("e%02d", i)),
			Engine:               aws.String("mysql"),
			DBInstanceClass:      aws.String("db.t3.micro"),
		}); err != nil {
			t.Fatalf("CreateDBInstance %d: %v", i, err)
		}
	}

	all, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{})
	if err != nil {
		t.Fatalf("DescribeEvents: %v", err)
	}

	if len(all.Events) != n || all.Marker != nil {
		t.Fatalf("got %d events (marker %v), want %d on one page", len(all.Events), all.Marker, n)
	}

	one, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{
		SourceType: rdstypes.SourceTypeDbInstance, SourceIdentifier: aws.String("e07"),
		EventCategories: []string{"creation"},
	})
	if err != nil || len(one.Events) != 1 || aws.ToString(one.Events[0].Message) != "DB instance created" {
		t.Fatalf("filtered DescribeEvents: %+v, err %v", one, err)
	}

	// A window entirely in the past excludes everything.
	past := time.Now().Add(-48 * time.Hour)

	old, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{
		StartTime: aws.Time(past), EndTime: aws.Time(past.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("DescribeEvents(past window): %v", err)
	}

	if len(old.Events) != 0 {
		t.Fatalf("past window: got %d events, want 0", len(old.Events))
	}

	page, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{MaxRecords: aws.Int32(20)})
	if err != nil {
		t.Fatalf("DescribeEvents page 1: %v", err)
	}

	if len(page.Events) != 20 || aws.ToString(page.Marker) == "" {
		t.Fatalf("page 1: %d events, marker %q", len(page.Events), aws.ToString(page.Marker))
	}

	if got := aws.ToString(page.Events[0].SourceIdentifier); got != "e00" {
		t.Fatalf("oldest event first: got %q, want e00", got)
	}

	next, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{MaxRecords: aws.Int32(20), Marker: page.Marker})
	if err != nil {
		t.Fatalf("DescribeEvents page 2: %v", err)
	}

	if len(next.Events) != 5 || aws.ToString(next.Events[0].SourceIdentifier) != "e20" || next.Marker != nil {
		t.Fatalf("page 2: %d events, marker %v", len(next.Events), next.Marker)
	}

	// MaxRecords outside the documented [20, 100] range is rejected.
	for _, bad := range []int32{19, 101} {
		_, err := client.DescribeEvents(ctx, &awsrds.DescribeEventsInput{MaxRecords: aws.Int32(bad)})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
			t.Fatalf("MaxRecords=%d: want InvalidParameterValue, got %v", bad, err)
		}
	}
	// SourceIdentifier without SourceType, and an undocumented SourceType, are
	// rejected like real RDS.
	cases := []struct {
		in   *awsrds.DescribeEventsInput
		code string
	}{
		{&awsrds.DescribeEventsInput{SourceIdentifier: aws.String("e00")}, "InvalidParameterCombination"},
		{&awsrds.DescribeEventsInput{SourceType: rdstypes.SourceType("db-bogus")}, "InvalidParameterValue"},
	}

	for _, tc := range cases {
		_, err := client.DescribeEvents(ctx, tc.in)

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != tc.code {
			t.Fatalf("DescribeEvents %+v: want %s, got %v", tc.in, tc.code, err)
		}
	}
}

// TestWireDescribeEventsRejectsMalformedTimes: a raw query client sending a
// timestamp or Duration that does not parse gets InvalidParameterValue, not
// the default window. The SourceIdentifier-without-SourceType rule, now
// enforced by the provider, still surfaces as InvalidParameterCombination.
func TestWireDescribeEventsRejectsMalformedTimes(t *testing.T) {
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{RDS: cloudemu.NewAWS().RDS}))
	t.Cleanup(ts.Close)

	cases := []struct {
		name     string
		extra    url.Values
		wantCode string
	}{
		{"bad StartTime", url.Values{"StartTime": {"garbage"}}, "InvalidParameterValue"},
		{"bad EndTime", url.Values{"EndTime": {"2025-13-45"}}, "InvalidParameterValue"},
		{"bad Duration", url.Values{"Duration": {"ten"}}, "InvalidParameterValue"},
		{"id without type", url.Values{"SourceIdentifier": {"db1"}}, "InvalidParameterCombination"},
		{"bogus type", url.Values{"SourceType": {"db-bogus"}}, "InvalidParameterValue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"Action": {"DescribeEvents"}, "Version": {"2014-10-31"}}
			for k, v := range tc.extra {
				form[k] = v
			}

			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL,
				strings.NewReader(form.Encode()))
			if err != nil {
				t.Fatal(err)
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "<Code>"+tc.wantCode+"</Code>") {
				t.Fatalf("status %d body %s, want 400 %s", resp.StatusCode, body, tc.wantCode)
			}
		})
	}
}
