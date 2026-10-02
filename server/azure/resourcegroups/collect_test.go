package resourcegroups_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/azure/resourcegroups"
)

// stubPurger records its calls into a shared log.
type stubPurger struct {
	name  string
	phase int
	err   error
	log   *[]string
}

func (s *stubPurger) PurgeResourceGroup(_ context.Context, _, rg string) error {
	*s.log = append(*s.log, s.name+":"+rg)
	return s.err
}

type phasedPurger struct{ stubPurger }

func (p *phasedPurger) PurgePhase() int { return p.phase }

// notPurger is a registered handler that owns no resource-group state.
type notPurger struct{}

func TestCollectPurgersOrdersByPhase(t *testing.T) {
	var log []string

	handlers := []any{
		&stubPurger{name: "default-a", log: &log},
		&phasedPurger{stubPurger{name: "network", phase: resourcegroups.PhaseNetwork, log: &log}},
		notPurger{},
		&phasedPurger{stubPurger{name: "consumer", phase: resourcegroups.PhaseNetworkConsumers, log: &log}},
		&phasedPurger{stubPurger{name: "compute", phase: resourcegroups.PhaseCompute, log: &log}},
		&stubPurger{name: "default-b", log: &log},
	}

	purgers := resourcegroups.CollectPurgers(handlers)
	if len(purgers) != 5 {
		t.Fatalf("collected %d purgers, want 5", len(purgers))
	}

	for _, p := range purgers {
		_ = p.PurgeResourceGroup(context.Background(), "s", "g")
	}

	want := "compute:g,default-a:g,default-b:g,consumer:g,network:g"
	if got := strings.Join(log, ","); got != want {
		t.Errorf("purge order = %s, want %s", got, want)
	}
}

// TestCascadeContinuesPastFailingPurger pins that one failing purger (for
// example a handler whose driver lacks the purge capability) neither fails
// the group delete nor stops the purgers after it.
func TestCascadeContinuesPastFailingPurger(t *testing.T) {
	var log []string

	h := resourcegroups.New(nil)
	h.SetPurgers([]resourcegroups.ResourceGroupPurger{
		&stubPurger{name: "broken", err: errors.New("driver cannot purge"), log: &log},
		&stubPurger{name: "ok", log: &log},
	})

	put := httptest.NewRequest(http.MethodPut, "/subscriptions/s/resourceGroups/g1", strings.NewReader(`{"location":"eastus"}`))
	h.ServeHTTP(httptest.NewRecorder(), put)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/subscriptions/s/resourceGroups/g1", http.NoBody))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("DELETE status = %d, want 202", rec.Code)
	}

	if got := strings.Join(log, ","); got != "broken:g1,ok:g1" {
		t.Errorf("purgers run = %s, want broken:g1,ok:g1", got)
	}
}
