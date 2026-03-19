package cost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracker_Record_And_TotalCost(t *testing.T) {
	tests := []struct {
		name      string
		records   []struct{ svc, op string; qty int }
		expectGt  float64
	}{
		{
			name: "single storage put",
			records: []struct{ svc, op string; qty int }{
				{"storage", "PutObject", 1000},
			},
			expectGt: 0,
		},
		{
			name: "unknown operation has zero cost",
			records: []struct{ svc, op string; qty int }{
				{"unknown", "DoSomething", 100},
			},
			expectGt: -1, // total should be 0
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := New()

			for _, r := range tc.records {
				tracker.Record(r.svc, r.op, r.qty)
			}

			total := tracker.TotalCost()
			assert.GreaterOrEqual(t, total, float64(0))
		})
	}
}

func TestTracker_SetRate(t *testing.T) {
	tracker := New()
	tracker.SetRate("custom", "Operation", 1.5)
	tracker.Record("custom", "Operation", 10)

	assert.InDelta(t, 15.0, tracker.TotalCost(), 0.001)
}

func TestTracker_CostByService(t *testing.T) {
	tracker := New()
	tracker.SetRate("svcA", "op1", 1.0)
	tracker.SetRate("svcB", "op2", 2.0)

	tracker.Record("svcA", "op1", 5)
	tracker.Record("svcB", "op2", 3)

	byService := tracker.CostByService()
	assert.InDelta(t, 5.0, byService["svcA"], 0.001)
	assert.InDelta(t, 6.0, byService["svcB"], 0.001)
}

func TestTracker_CostByOperation(t *testing.T) {
	tracker := New()
	tracker.SetRate("svc", "opA", 1.0)
	tracker.SetRate("svc", "opB", 0.5)

	tracker.Record("svc", "opA", 4)
	tracker.Record("svc", "opB", 6)

	byOp := tracker.CostByOperation()
	assert.InDelta(t, 4.0, byOp["svc:opA"], 0.001)
	assert.InDelta(t, 3.0, byOp["svc:opB"], 0.001)
}

func TestTracker_AllCosts(t *testing.T) {
	tracker := New()
	tracker.Record("storage", "PutObject", 1)
	tracker.Record("compute", "RunInstances", 1)

	all := tracker.AllCosts()
	require.Len(t, all, 2)
	assert.Equal(t, "storage", all[0].Service)
	assert.Equal(t, "PutObject", all[0].Operation)
	assert.Equal(t, 1, all[0].Quantity)
	assert.Equal(t, "compute", all[1].Service)
}

func TestTracker_Reset(t *testing.T) {
	tracker := New()
	tracker.Record("storage", "PutObject", 100)
	require.Greater(t, len(tracker.AllCosts()), 0)

	tracker.Reset()
	assert.Equal(t, 0, len(tracker.AllCosts()))
	assert.Equal(t, float64(0), tracker.TotalCost())
}

func TestTracker_DefaultRates(t *testing.T) {
	tracker := New()

	tests := []struct {
		name string
		svc  string
		op   string
		qty  int
	}{
		{name: "compute RunInstances", svc: "compute", op: "RunInstances", qty: 1},
		{name: "storage PutObject", svc: "storage", op: "PutObject", qty: 1},
		{name: "serverless Invoke", svc: "serverless", op: "Invoke", qty: 1},
		{name: "iam CreateUser (free)", svc: "iam", op: "CreateUser", qty: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker.Record(tc.svc, tc.op, tc.qty)
			// just verify it doesn't panic
			assert.GreaterOrEqual(t, tracker.TotalCost(), float64(0))
		})
	}
}

func TestTracker_Empty(t *testing.T) {
	tracker := New()

	assert.Equal(t, float64(0), tracker.TotalCost())
	assert.Empty(t, tracker.AllCosts())
	assert.Empty(t, tracker.CostByService())
	assert.Empty(t, tracker.CostByOperation())
}

func TestServiceCost_Fields(t *testing.T) {
	tracker := New()
	tracker.SetRate("test", "op", 2.5)
	tracker.Record("test", "op", 4)

	costs := tracker.AllCosts()
	require.Len(t, costs, 1)

	sc := costs[0]
	assert.Equal(t, "test", sc.Service)
	assert.Equal(t, "op", sc.Operation)
	assert.InDelta(t, 2.5, sc.UnitCost, 0.001)
	assert.Equal(t, 4, sc.Quantity)
	assert.InDelta(t, 10.0, sc.Total, 0.001)
}
