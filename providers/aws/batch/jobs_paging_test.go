package batch_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func listJobs(m *batch.Mock, in driver.ListJobsInput) ([]driver.Job, error) {
	out, err := m.ListJobs(context.Background(), in)
	if err != nil {
		return nil, err
	}

	return out.Jobs, nil
}

func TestListJobsCursorIsStable(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	for _, n := range []string{"a", "b", "c", "d"} {
		submit(t, m, driver.SubmitJobInput{JobName: n})
	}

	two := int32(2)
	in := driver.ListJobsInput{JobQueue: "q", JobStatus: driver.JobStatusSucceeded, MaxResults: &two}

	p1, err := m.ListJobs(ctx, in)
	if err != nil || len(p1.Jobs) != 2 || p1.NextToken == "" || p1.Jobs[0].JobName != "d" || p1.Jobs[1].JobName != "c" {
		t.Fatalf("page 1 = %+v, %v", p1, err)
	}

	// A job submitted between pages must not shift the next page.
	submit(t, m, driver.SubmitJobInput{JobName: "e"})

	in.NextToken = p1.NextToken

	p2, err := m.ListJobs(ctx, in)
	if err != nil || len(p2.Jobs) != 2 || p2.NextToken != "" || p2.Jobs[0].JobName != "b" || p2.Jobs[1].JobName != "a" {
		t.Fatalf("page 2 = %+v, %v", p2, err)
	}

	in.NextToken = "!!"
	if _, err := m.ListJobs(ctx, in); !errors.IsInvalidArgument(err) {
		t.Fatalf("bad token: want InvalidArgument, got %v", err)
	}

	zero := int32(0)
	if _, err := m.ListJobs(ctx, driver.ListJobsInput{JobQueue: "q", MaxResults: &zero}); !errors.IsInvalidArgument(err) {
		t.Fatalf("maxResults 0: want InvalidArgument, got %v", err)
	}

	// Array children page by index.
	size := int32(5)
	arr := submit(t, m, driver.SubmitJobInput{JobName: "arr", ArraySize: &size})
	cin := driver.ListJobsInput{ArrayJobID: arr.JobID, JobStatus: driver.JobStatusSucceeded, MaxResults: &two}

	var idx []int32

	for {
		page, err := m.ListJobs(ctx, cin)
		if err != nil {
			t.Fatal(err)
		}

		for i := range page.Jobs {
			idx = append(idx, *page.Jobs[i].ArrayProperties.Index)
		}

		if page.NextToken == "" {
			break
		}

		cin.NextToken = page.NextToken
	}

	if len(idx) != 5 || idx[0] != 0 || idx[4] != 4 {
		t.Fatalf("children pages = %v, want 0..4", idx)
	}
}

func TestTickPublishesUnpolledTransitions(t *testing.T) {
	m, fc := newAsyncMock(t)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	setupQueue(t, m)

	submit(t, m, driver.SubmitJobInput{})

	if m.Tick(fc.Now()) {
		t.Fatal("Tick right after submit reported a change")
	}

	fc.Advance(2 * time.Second)

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick after 2s reported no change")
	}

	if got := pub.statuses(); len(got) != 2 || got[0] != "RUNNABLE" || got[1] != "STARTING" {
		t.Fatalf("events after Tick = %v, want [RUNNABLE STARTING]", got)
	}

	fc.Advance(10 * time.Second)
	m.Tick(fc.Now())

	if got := pub.statuses(); len(got) != 4 || got[3] != "SUCCEEDED" {
		t.Fatalf("events = %v, want [... RUNNING SUCCEEDED]", got)
	}

	if m.Tick(fc.Now()) {
		t.Fatal("Tick on a finished job reported a change")
	}
}

func TestTagValidation(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	j := submit(t, m, driver.SubmitJobInput{})

	bad := []map[string]string{
		{"aws:owner": "x"},
		{"AWS:owner": "x"},
		{"": "x"},
		{strings.Repeat("k", 129): "x"},
		{"k": strings.Repeat("v", 257)},
	}

	for i, tags := range bad {
		if err := m.TagResource(ctx, j.JobARN, tags); !errors.IsInvalidArgument(err) {
			t.Errorf("case %d: TagResource want InvalidArgument, got %v", i, err)
		}

		if _, err := m.SubmitJob(ctx, driver.SubmitJobInput{
			JobName: "t", JobQueue: "q", JobDefinition: "jd", Tags: tags,
		}); !errors.IsInvalidArgument(err) {
			t.Errorf("case %d: SubmitJob want InvalidArgument, got %v", i, err)
		}

		if _, err := m.CreateSchedulingPolicy(ctx, driver.CreateSchedulingPolicyInput{Name: "sp", Tags: tags}); !errors.IsInvalidArgument(err) {
			t.Errorf("case %d: CreateSchedulingPolicy want InvalidArgument, got %v", i, err)
		}
	}

	if err := m.TagResource(ctx, j.JobARN, map[string]string{"team": "a"}); err != nil {
		t.Fatalf("TagResource(job): %v", err)
	}

	if tags, err := m.ListTagsForResource(ctx, j.JobARN); err != nil || tags["team"] != "a" {
		t.Fatalf("ListTagsForResource(job) = %v, %v", tags, err)
	}

	// Child jobs of an array job can't be tagged.
	size := int32(2)
	arr := submit(t, m, driver.SubmitJobInput{JobName: "arr", ArraySize: &size})

	if err := m.TagResource(ctx, arr.JobARN+":0", map[string]string{"k": "v"}); !errors.IsNotFound(err) {
		t.Fatalf("tagging an array child: want NotFound, got %v", err)
	}

	// The merged tag set is capped at 50.
	many := map[string]string{}
	for i := 0; i < 50; i++ {
		many["k"+strings.Repeat("x", i)] = "v"
	}

	if err := m.TagResource(ctx, j.JobARN, many); !errors.IsInvalidArgument(err) {
		t.Fatalf("51 total tags: want InvalidArgument, got %v", err)
	}

	if tags, _ := m.ListTagsForResource(ctx, j.JobARN); len(tags) != 1 {
		t.Fatalf("rejected TagResource changed tags: %v", tags)
	}
}

// TestConcurrentJobOperations runs submit, describe, list, cancel, terminate,
// tag and Tick at once (run with -race), with an event target that calls back
// into the mock, which must not deadlock.
func TestConcurrentJobOperations(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)
	m.SetEventPublisher(callbackPublisher{m: m})

	ctx := context.Background()

	var wg sync.WaitGroup

	for w := 0; w < 6; w++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < 20; i++ {
				j, err := m.SubmitJob(ctx, driver.SubmitJobInput{JobName: "c", JobQueue: "q", JobDefinition: "jd"})
				if err != nil {
					t.Error(err)

					return
				}

				_, _ = m.DescribeJobs(ctx, []string{j.JobID})
				_, _ = m.ListJobs(ctx, driver.ListJobsInput{JobQueue: "q"})
				_ = m.TagResource(ctx, j.JobARN, map[string]string{"k": "v"})

				if i%2 == 0 {
					_ = m.CancelJob(ctx, j.JobID, "r")
				} else {
					_ = m.TerminateJob(ctx, j.JobID, "r")
				}

				fc.Advance(100 * time.Millisecond)
				m.Tick(fc.Now())
			}
		}()
	}

	wg.Wait()
}

// callbackPublisher re-enters the mock from event delivery, like an
// EventBridge rule whose target calls Batch.
type callbackPublisher struct{ m *batch.Mock }

func (p callbackPublisher) PublishServiceEvent(ctx context.Context, _, _ string, _ any, resources []string) {
	_, _ = p.m.DescribeJobs(ctx, resources)
	_, _ = p.m.ListJobs(ctx, driver.ListJobsInput{JobQueue: "q", JobStatus: driver.JobStatusFailed})
}
