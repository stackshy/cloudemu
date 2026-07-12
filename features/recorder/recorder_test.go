package recorder

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecorder_Record_And_Calls(t *testing.T) {
	r := New()
	r.Record("s3", "PutObject", "input1", "output1", nil, 10*time.Millisecond)
	r.Record("ec2", "RunInstances", "input2", "output2", nil, 20*time.Millisecond)

	calls := r.Calls()
	assert.Len(t, calls, 2)
	assert.Equal(t, "s3", calls[0].Service)
	assert.Equal(t, "PutObject", calls[0].Operation)
	assert.Equal(t, "input1", calls[0].Input)
	assert.Equal(t, "output1", calls[0].Output)
	assert.Nil(t, calls[0].Error)
}

func TestRecorder_CallCount(t *testing.T) {
	r := New()
	assert.Equal(t, 0, r.CallCount())

	r.Record("s3", "GetObject", nil, nil, nil, 0)
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	assert.Equal(t, 2, r.CallCount())
}

func TestRecorder_CallsFor(t *testing.T) {
	r := New()
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	r.Record("ec2", "RunInstances", nil, nil, nil, 0)
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	r.Record("s3", "GetObject", nil, nil, nil, 0)

	tests := []struct {
		name      string
		service   string
		operation string
		expectN   int
	}{
		{name: "s3 PutObject", service: "s3", operation: "PutObject", expectN: 2},
		{name: "ec2 RunInstances", service: "ec2", operation: "RunInstances", expectN: 1},
		{name: "s3 GetObject", service: "s3", operation: "GetObject", expectN: 1},
		{name: "nonexistent", service: "lambda", operation: "Invoke", expectN: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := r.CallsFor(tc.service, tc.operation)
			assert.Len(t, calls, tc.expectN)
		})
	}
}

func TestRecorder_CallCountFor(t *testing.T) {
	r := New()
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	r.Record("s3", "PutObject", nil, nil, nil, 0)

	assert.Equal(t, 2, r.CallCountFor("s3", "PutObject"))
	assert.Equal(t, 0, r.CallCountFor("s3", "DeleteObject"))
}

func TestRecorder_Reset(t *testing.T) {
	r := New()
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	require.Equal(t, 1, r.CallCount())

	r.Reset()
	assert.Equal(t, 0, r.CallCount())
	assert.Empty(t, r.Calls())
}

func TestRecorder_LastCall(t *testing.T) {
	tests := []struct {
		name     string
		records  int
		expectOp string
		expectNl bool
	}{
		{name: "no calls", records: 0, expectNl: true},
		{name: "one call", records: 1, expectOp: "op-0", expectNl: false},
		{name: "multiple calls", records: 3, expectOp: "op-2", expectNl: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			for i := 0; i < tc.records; i++ {
				r.Record("svc", fmt.Sprintf("op-%d", i), nil, nil, nil, 0)
			}

			last := r.LastCall()

			assert.Equal(t, tc.expectNl, last == nil)

			if last != nil {
				assert.Equal(t, tc.expectOp, last.Operation)
			}
		})
	}
}

func TestRecorder_RecordWithError(t *testing.T) {
	r := New()
	testErr := fmt.Errorf("something failed")
	r.Record("s3", "PutObject", nil, nil, testErr, 5*time.Millisecond)

	calls := r.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, testErr, calls[0].Error)
	assert.Equal(t, 5*time.Millisecond, calls[0].Duration)
}

func TestMatcher_ForServiceAndOperation(t *testing.T) {
	r := New()
	r.Record("s3", "PutObject", nil, nil, nil, 0)
	r.Record("ec2", "RunInstances", nil, nil, nil, 0)
	r.Record("s3", "GetObject", nil, nil, nil, 0)

	m := NewMatcher(t, r)
	m.ForService("s3").Count(2)
	m.ForService("ec2").Count(1)
	m.ForService("lambda").Count(0)
	m.ForOperation("PutObject").Count(1)
}

func TestMatcher_HasCalls_NoCalls(t *testing.T) {
	r := New()
	r.Record("s3", "Put", nil, nil, nil, 0)

	m := NewMatcher(t, r)
	m.HasCalls()
	m.ForService("s3").HasCalls()
	m.ForService("lambda").NoCalls()
}

func TestMatcher_NoErrors_AllErrored(t *testing.T) {
	r := New()
	r.Record("s3", "Put", nil, nil, nil, 0)

	m := NewMatcher(t, r)
	m.NoErrors()

	r2 := New()
	r2.Record("s3", "Put", nil, nil, fmt.Errorf("fail"), 0)

	m2 := NewMatcher(t, r2)
	m2.AllErrored()
}

func TestMatcher_String(t *testing.T) {
	r := New()
	r.Record("s3", "Put", nil, nil, nil, 0)

	m := NewMatcher(t, r)
	assert.Equal(t, "Matcher{calls: 1}", m.String())
}
