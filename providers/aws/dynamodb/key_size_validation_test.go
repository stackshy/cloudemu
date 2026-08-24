package dynamodb

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// createSingleKeyTable makes a table keyed only on "pk" (no sort key), so an
// item needs just the partition key.
func createSingleKeyTable(m *Mock, name string) {
	_ = m.CreateTable(context.Background(), driver.TableConfig{Name: name, PartitionKey: "pk"})
}

func TestPutItemEmptyStringKeyRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.PutItem(context.Background(), "t", map[string]any{"pk": ""})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty key, got %v", err)
	}

	if !strings.Contains(err.Error(), "cannot contain an empty string value") {
		t.Fatalf("message = %q, want empty-string-key wording", err.Error())
	}
}

func TestPutItemEmptyBinaryKeyRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.PutItem(context.Background(), "t", map[string]any{"pk": []byte{}})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty binary key, got %v", err)
	}

	if !strings.Contains(err.Error(), "cannot contain an empty binary value") {
		t.Fatalf("message = %q, want empty-binary-key wording", err.Error())
	}
}

func TestPutItemOversizedRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.PutItem(context.Background(), "t", map[string]any{
		"pk":   "k1",
		"blob": strings.Repeat("a", maxItemSizeBytes+1),
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for oversized item, got %v", err)
	}

	if !strings.Contains(err.Error(), "maximum allowed size") {
		t.Fatalf("message = %q, want item-size wording", err.Error())
	}
}

func TestBatchPutOversizedRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.BatchPutItems(context.Background(), "t", []map[string]any{
		{"pk": "ok", "small": "v"},
		{"pk": "big", "blob": strings.Repeat("a", maxItemSizeBytes+1)},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for oversized batch item, got %v", err)
	}

	// The oversized batch must be rejected wholesale — the valid item must not
	// have been written either.
	if _, getErr := m.GetItem(context.Background(), "t", map[string]any{"pk": "ok"}); getErr == nil {
		t.Fatal("valid item should not persist when the batch is rejected")
	}
}

func TestDeleteItemEmptyKeyRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.DeleteItem(context.Background(), "t", map[string]any{"pk": ""})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty delete key, got %v", err)
	}
}

func TestUpdateItemEmptyKeyRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	_, err := m.UpdateItem(context.Background(), driver.UpdateItemInput{
		Table:            "t",
		Key:              map[string]any{"pk": ""},
		UpdateExpression: "SET v = :v",
		ExprValues:       map[string]any{":v": "x"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty update key, got %v", err)
	}
}

func TestUpdateItemOversizedRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	if err := m.PutItem(context.Background(), "t", map[string]any{"pk": "k1"}); err != nil {
		t.Fatalf("seed put should succeed: %v", err)
	}

	_, err := m.UpdateItem(context.Background(), driver.UpdateItemInput{
		Table:            "t",
		Key:              map[string]any{"pk": "k1"},
		UpdateExpression: "SET blob = :b",
		ExprValues:       map[string]any{":b": strings.Repeat("a", maxItemSizeBytes+1)},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for oversized update result, got %v", err)
	}

	if !strings.Contains(err.Error(), "maximum allowed size") {
		t.Fatalf("message = %q, want item-size wording", err.Error())
	}

	// The oversized update must not have persisted; the item stays as seeded.
	got, getErr := m.GetItem(context.Background(), "t", map[string]any{"pk": "k1"})
	if getErr != nil {
		t.Fatalf("seed item should still be readable: %v", getErr)
	}

	if _, ok := got["blob"]; ok {
		t.Fatal("oversized attribute should not have been written")
	}
}

func TestTransactWriteEmptyKeyRejected(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	err := m.TransactWriteItems(context.Background(), "t",
		[]map[string]any{{"pk": ""}}, nil)
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for empty transact put key, got %v", err)
	}
}

func TestPutItemNonEmptyKeySucceeds(t *testing.T) {
	m := newTestMock()
	createSingleKeyTable(m, "t")

	if err := m.PutItem(context.Background(), "t", map[string]any{"pk": "good"}); err != nil {
		t.Fatalf("valid key put should succeed: %v", err)
	}
}
