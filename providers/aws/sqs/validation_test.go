package sqs

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// assertCode fails unless err carries the wanted canonical code.
func assertCode(t *testing.T, err error, want errors.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected %s error, got nil", want)
	}

	if got := errors.GetCode(err); got != want {
		t.Fatalf("error code = %s, want %s (err: %v)", got, want, err)
	}
}

func TestCreateQueueNameRules(t *testing.T) {
	tests := []struct {
		name    string
		cfg     driver.QueueConfig
		wantErr bool
	}{
		{name: "spaces and punctuation", cfg: driver.QueueConfig{Name: "bad name with spaces!"}, wantErr: true},
		{name: "81 characters", cfg: driver.QueueConfig{Name: strings.Repeat("a", 81)}, wantErr: true},
		{name: "80 characters", cfg: driver.QueueConfig{Name: strings.Repeat("a", 80)}},
		{name: "dot in standard name", cfg: driver.QueueConfig{Name: "orders.fifo"}, wantErr: true},
		{name: "fifo base of 76", cfg: driver.QueueConfig{Name: strings.Repeat("a", 76) + ".fifo", FIFO: true}, wantErr: true},
		{name: "fifo base of 75", cfg: driver.QueueConfig{Name: strings.Repeat("a", 75) + ".fifo", FIFO: true}},
		{name: "fifo with bad charset", cfg: driver.QueueConfig{Name: "a b.fifo", FIFO: true}, wantErr: true},
		{name: "hyphen and underscore", cfg: driver.QueueConfig{Name: "My_Queue-1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			_, err := m.CreateQueue(context.Background(), tc.cfg)
			if tc.wantErr {
				assertCode(t, err, errors.InvalidArgument)
				return
			}

			requireNoError(t, err)
		})
	}
}

func TestSendMessageBodyRules(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		sentinel error
	}{
		{name: "empty body", body: "", sentinel: driver.ErrMissingParameter},
		{name: "control character", body: "bad\x01", sentinel: driver.ErrInvalidMessageContents},
		{name: "invalid utf-8", body: "bad\xff", sentinel: driver.ErrInvalidMessageContents},
		{name: "noncharacter U+FFFE", body: "bad￾", sentinel: driver.ErrInvalidMessageContents},
		{name: "tab and newline allowed", body: "ok\t\n\r"},
		{name: "astral plane allowed", body: "ok \U0001F600"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			q := createStdQueue(m, "body-q")

			_, err := m.SendMessage(context.Background(), driver.SendMessageInput{QueueURL: q.URL, Body: tc.body})
			if tc.sentinel == nil {
				requireNoError(t, err)
				return
			}

			assertCode(t, err, errors.InvalidArgument)

			if !stderrors.Is(err, tc.sentinel) {
				t.Fatalf("error = %v, want %v", err, tc.sentinel)
			}
		})
	}
}

func TestFIFORejectsPerMessageDelay(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createFIFOQueue(m, "delay.fifo")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL, Body: "x", GroupID: "g", DeduplicationID: "d", DelaySeconds: 10,
	})
	assertCode(t, err, errors.InvalidArgument)

	// Without a per-message delay the same send succeeds.
	_, err = m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL, Body: "x", GroupID: "g", DeduplicationID: "d",
	})
	requireNoError(t, err)
}

func TestSendMessageDelayRange(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "delay-range")

	for _, delay := range []int{-1, 901} {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x", DelaySeconds: delay})
		assertCode(t, err, errors.InvalidArgument)
	}

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x", DelaySeconds: 900})
	requireNoError(t, err)
}

func TestSendMessageAttributeTypes(t *testing.T) {
	tests := []struct {
		name    string
		attr    driver.MessageAttributeValue
		wantErr bool
	}{
		{name: "bogus type", attr: driver.MessageAttributeValue{DataType: "BogusType", StringValue: "x"}, wantErr: true},
		{name: "empty type", attr: driver.MessageAttributeValue{StringValue: "x"}, wantErr: true},
		{name: "empty string value", attr: driver.MessageAttributeValue{DataType: "String"}, wantErr: true},
		{name: "empty binary value", attr: driver.MessageAttributeValue{DataType: "Binary"}, wantErr: true},
		{name: "custom label", attr: driver.MessageAttributeValue{DataType: "String.custom", StringValue: "x"}},
		{name: "number", attr: driver.MessageAttributeValue{DataType: "Number", StringValue: "42"}},
		{name: "binary", attr: driver.MessageAttributeValue{DataType: "Binary", BinaryValue: []byte{1}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			q := createStdQueue(m, "attr-q")

			_, err := m.SendMessage(context.Background(), driver.SendMessageInput{
				QueueURL: q.URL, Body: "x", MessageAttributes: map[string]driver.MessageAttributeValue{"a": tc.attr},
			})
			if tc.wantErr {
				assertCode(t, err, errors.InvalidArgument)
				return
			}

			requireNoError(t, err)
		})
	}
}

func TestReceiveMessagesRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name  string
		input driver.ReceiveMessageInput
	}{
		{name: "max 11", input: driver.ReceiveMessageInput{MaxMessages: 11}},
		{name: "max -1", input: driver.ReceiveMessageInput{MaxMessages: -1}},
		{name: "explicit max 0", input: driver.ReceiveMessageInput{MaxMessages: 0, MaxMessagesSet: true}},
		{name: "wait 21", input: driver.ReceiveMessageInput{WaitTimeSeconds: 21}},
		{name: "wait -1", input: driver.ReceiveMessageInput{WaitTimeSeconds: -1}},
		{name: "visibility 43201", input: driver.ReceiveMessageInput{VisibilityTimeout: 43201}},
		{name: "visibility -1", input: driver.ReceiveMessageInput{VisibilityTimeout: -1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			q := createStdQueue(m, "recv-range")
			tc.input.QueueURL = q.URL

			_, err := m.ReceiveMessages(context.Background(), tc.input)
			assertCode(t, err, errors.InvalidArgument)
		})
	}

	t.Run("with options max 11", func(t *testing.T) {
		m, _ := newTestMock()
		q := createStdQueue(m, "recv-opts")

		_, err := m.ReceiveMessagesWithOptions(context.Background(), q.URL, driver.ReceiveOptions{MaxMessages: 11})
		assertCode(t, err, errors.InvalidArgument)
	})
}

// An explicit VisibilityTimeout of 0 on receive must leave the message
// visible, not fall back to the queue default of 30 seconds.
func TestReceiveExplicitZeroVisibility(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "vis-zero")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x"})
	requireNoError(t, err)

	first, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
		QueueURL: q.URL, VisibilityTimeout: 0, VisibilityTimeoutSet: true,
	})
	requireNoError(t, err)
	assertEqual(t, 1, len(first))

	again, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
	requireNoError(t, err)
	assertEqual(t, 1, len(again))
}

// An explicit WaitTimeSeconds of 0 forces a short poll even when the queue
// has a long-poll default.
func TestReceiveExplicitZeroWaitShortPolls(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "wait-zero", ReceiveMessageWaitTimeSeconds: 20})
	requireNoError(t, err)

	start := time.Now()

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
		QueueURL: q.URL, WaitTimeSeconds: 0, WaitTimeSecondsSet: true,
	})
	requireNoError(t, err)
	assertEqual(t, 0, len(msgs))

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("explicit WaitTimeSeconds=0 long-polled for %s", elapsed)
	}
}

// A batch entry fails with the same code a single SendMessage would get.
func TestSendMessageBatchPerEntryCodes(t *testing.T) {
	m, _ := newTestMock()
	q := createStdQueue(m, "batch-codes")

	res, err := m.SendMessageBatch(context.Background(), q.URL, []driver.BatchSendEntry{
		{ID: "ok", Body: "fine"},
		{ID: "empty", Body: ""},
		{ID: "ctrl", Body: "bad\x01"},
		{ID: "attr", Body: "x", MessageAttributes: map[string]driver.MessageAttributeValue{
			"a": {DataType: "BogusType", StringValue: "x"},
		}},
	})
	requireNoError(t, err)
	assertEqual(t, 1, len(res.Successful))

	want := map[string]string{
		"empty": "MissingParameter",
		"ctrl":  "InvalidMessageContents",
		"attr":  "InvalidParameterValue",
	}

	assertEqual(t, len(want), len(res.Failed))

	for _, f := range res.Failed {
		assertEqual(t, want[f.ID], f.Code)

		if strings.HasPrefix(f.Message, "InvalidArgument") {
			t.Errorf("entry %s message leaks the internal code: %q", f.ID, f.Message)
		}
	}
}

// An explicit per-message DelaySeconds of 0 overrides the queue delay, while
// an omitted value still inherits it.
func TestSendMessageExplicitZeroDelayOverridesQueue(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "delayed-q", DelaySeconds: 60})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "now", DelaySecondsSet: true})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "later"})
	requireNoError(t, err)

	// Hold the first message long enough that it can't reappear below.
	got, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10, VisibilityTimeout: 600})
	requireNoError(t, err)
	assertEqual(t, 1, len(got))

	if len(got) == 1 {
		assertEqual(t, "now", got[0].Body)
	}

	fc.Advance(61 * time.Second)

	got, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 1, len(got))

	if len(got) == 1 {
		assertEqual(t, "later", got[0].Body)
	}
}

// A FIFO send with no MessageGroupId is MissingParameter. A missing
// MessageDeduplicationId without content-based dedup is InvalidParameterValue.
func TestFIFOMissingGroupAndDedupCodes(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createFIFOQueue(m, "codes.fifo")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x", DeduplicationID: "d"})
	if !stderrors.Is(err, driver.ErrMissingMessageGroupID) {
		t.Fatalf("missing group: err = %v, want ErrMissingMessageGroupID", err)
	}

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x", GroupID: "g"})
	assertCode(t, err, errors.InvalidArgument)

	if stderrors.Is(err, driver.ErrMissingMessageGroupID) || stderrors.Is(err, driver.ErrMissingParameter) {
		t.Fatalf("missing dedup id must not be MissingParameter, got %v", err)
	}

	res, err := m.SendMessageBatch(ctx, q.URL, []driver.BatchSendEntry{
		{ID: "nogroup", Body: "x", DeduplicationID: "d"},
		{ID: "nodedup", Body: "x", GroupID: "g"},
	})
	requireNoError(t, err)

	want := map[string]string{"nogroup": "MissingParameter", "nodedup": "InvalidParameterValue"}
	for _, f := range res.Failed {
		assertEqual(t, want[f.ID], f.Code)
	}

	assertEqual(t, 2, len(res.Failed))
}
