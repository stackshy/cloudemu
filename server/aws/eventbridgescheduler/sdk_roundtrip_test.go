package eventbridgescheduler_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssched "github.com/aws/aws-sdk-go-v2/service/scheduler"
	stypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awssched.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Scheduler: cloud.Scheduler})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssched.NewFromConfig(cfg, func(o *awssched.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createScheduleInput(name, group string) *awssched.CreateScheduleInput {
	return &awssched.CreateScheduleInput{
		Name:               aws.String(name),
		GroupName:          aws.String(group),
		ScheduleExpression: aws.String("rate(1 hour)"),
		FlexibleTimeWindow: &stypes.FlexibleTimeWindow{Mode: stypes.FlexibleTimeWindowModeOff},
		Target: &stypes.Target{
			Arn:     aws.String("arn:aws:sqs:us-east-1:123456789012:my-queue"),
			RoleArn: aws.String("arn:aws:iam::123456789012:role/my-role"),
			Input:   aws.String(`{"hello":"world"}`),
		},
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateScheduleGroup(ctx, &awssched.CreateScheduleGroupInput{Name: aws.String("g1")})
	if err != nil {
		t.Fatalf("CreateScheduleGroup: %v", err)
	}

	created, err := c.CreateSchedule(ctx, createScheduleInput("s1", "g1"))
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	wantArn := "arn:aws:scheduler:us-east-1:123456789012:schedule/g1/s1"
	if aws.ToString(created.ScheduleArn) != wantArn {
		t.Fatalf("ScheduleArn = %q, want %q", aws.ToString(created.ScheduleArn), wantArn)
	}

	got, err := c.GetSchedule(ctx, &awssched.GetScheduleInput{
		Name:      aws.String("s1"),
		GroupName: aws.String("g1"),
	})
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}

	assertScheduleStable(t, got)

	// A second read returns identical computed fields — no drift.
	got2, err := c.GetSchedule(ctx, &awssched.GetScheduleInput{Name: aws.String("s1"), GroupName: aws.String("g1")})
	if err != nil {
		t.Fatalf("GetSchedule (2): %v", err)
	}

	if !got.CreationDate.Equal(*got2.CreationDate) || aws.ToString(got.Arn) != aws.ToString(got2.Arn) {
		t.Fatalf("computed fields drifted between reads")
	}
}

func assertScheduleStable(t *testing.T, got *awssched.GetScheduleOutput) {
	t.Helper()

	if aws.ToString(got.Arn) != "arn:aws:scheduler:us-east-1:123456789012:schedule/g1/s1" {
		t.Fatalf("Arn = %q", aws.ToString(got.Arn))
	}

	if got.State != stypes.ScheduleStateEnabled {
		t.Fatalf("State = %q, want ENABLED", got.State)
	}

	if got.FlexibleTimeWindow == nil || got.FlexibleTimeWindow.Mode != stypes.FlexibleTimeWindowModeOff {
		t.Fatalf("FlexibleTimeWindow not round-tripped: %+v", got.FlexibleTimeWindow)
	}

	if got.Target == nil || aws.ToString(got.Target.RoleArn) != "arn:aws:iam::123456789012:role/my-role" {
		t.Fatalf("Target not round-tripped verbatim: %+v", got.Target)
	}

	if aws.ToString(got.Target.Input) != `{"hello":"world"}` {
		t.Fatalf("Target.Input = %q", aws.ToString(got.Target.Input))
	}

	if got.CreationDate == nil || got.LastModificationDate == nil {
		t.Fatalf("timestamps missing")
	}
}

func TestUpdateAndDeleteSchedule(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateSchedule(ctx, createScheduleInput("s1", "default"))
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	_, err = c.UpdateSchedule(ctx, &awssched.UpdateScheduleInput{
		Name:               aws.String("s1"),
		GroupName:          aws.String("default"),
		Description:        aws.String("changed"),
		State:              stypes.ScheduleStateDisabled,
		ScheduleExpression: aws.String("rate(1 hour)"),
		FlexibleTimeWindow: &stypes.FlexibleTimeWindow{Mode: stypes.FlexibleTimeWindowModeOff},
		Target:             createScheduleInput("s1", "default").Target,
	})
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}

	got, err := c.GetSchedule(ctx, &awssched.GetScheduleInput{Name: aws.String("s1")})
	if err != nil {
		t.Fatalf("GetSchedule after update: %v", err)
	}

	if aws.ToString(got.Description) != "changed" || got.State != stypes.ScheduleStateDisabled {
		t.Fatalf("update not applied: desc=%q state=%q", aws.ToString(got.Description), got.State)
	}

	_, err = c.DeleteSchedule(ctx, &awssched.DeleteScheduleInput{Name: aws.String("s1")})
	if err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}

	_, err = c.GetSchedule(ctx, &awssched.GetScheduleInput{Name: aws.String("s1")})

	var nfe *stypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("GetSchedule after delete = %v, want ResourceNotFoundException", err)
	}
}

func TestScheduleGroupRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	created, err := c.CreateScheduleGroup(ctx, &awssched.CreateScheduleGroupInput{
		Name: aws.String("g1"),
		Tags: []stypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	if err != nil {
		t.Fatalf("CreateScheduleGroup: %v", err)
	}

	wantArn := "arn:aws:scheduler:us-east-1:123456789012:schedule-group/g1"
	if aws.ToString(created.ScheduleGroupArn) != wantArn {
		t.Fatalf("ScheduleGroupArn = %q, want %q", aws.ToString(created.ScheduleGroupArn), wantArn)
	}

	got, err := c.GetScheduleGroup(ctx, &awssched.GetScheduleGroupInput{Name: aws.String("g1")})
	if err != nil {
		t.Fatalf("GetScheduleGroup: %v", err)
	}

	if aws.ToString(got.Arn) != wantArn || got.State != stypes.ScheduleGroupStateActive {
		t.Fatalf("group read wrong: arn=%q state=%q", aws.ToString(got.Arn), got.State)
	}

	if got.CreationDate == nil || got.LastModificationDate == nil {
		t.Fatalf("group timestamps missing")
	}

	tags, err := c.ListTagsForResource(ctx, &awssched.ListTagsForResourceInput{ResourceArn: created.ScheduleGroupArn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("tags = %+v", tags.Tags)
	}
}

func TestDefaultScheduleGroupImplicit(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	got, err := c.GetScheduleGroup(ctx, &awssched.GetScheduleGroupInput{Name: aws.String("default")})
	if err != nil {
		t.Fatalf("GetScheduleGroup(default): %v", err)
	}

	if got.State != stypes.ScheduleGroupStateActive {
		t.Fatalf("default group state = %q, want ACTIVE", got.State)
	}
}

func TestListSchedules(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	for _, n := range []string{"a", "b"} {
		if _, err := c.CreateSchedule(ctx, createScheduleInput(n, "default")); err != nil {
			t.Fatalf("CreateSchedule %s: %v", n, err)
		}
	}

	out, err := c.ListSchedules(ctx, &awssched.ListSchedulesInput{})
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}

	if len(out.Schedules) != 2 {
		t.Fatalf("ListSchedules returned %d, want 2", len(out.Schedules))
	}

	if aws.ToString(out.Schedules[0].Target.Arn) == "" {
		t.Fatalf("schedule summary target arn missing")
	}
}
