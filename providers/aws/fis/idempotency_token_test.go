package fis_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/fis"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

func TestCreateExperimentTemplateClientToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := sampleCreateInput()

	first, err := m.CreateExperimentTemplate(ctx, in)
	requireNoError(t, err)

	retry, err := m.CreateExperimentTemplate(ctx, in)
	requireNoError(t, err)

	if retry.ID != first.ID {
		t.Fatalf("same-token retry id = %s, want original %s", retry.ID, first.ID)
	}

	in.ClientToken = "tok-2"

	other, err := m.CreateExperimentTemplate(ctx, in)
	requireNoError(t, err)

	if other.ID == first.ID {
		t.Fatalf("different token must mint a new template, got %s again", other.ID)
	}

	list, _, err := m.ListExperimentTemplates(ctx, driver.Page{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("template count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

func TestStartExperimentClientToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	tpl := mustTemplate(t, m)

	in := &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID, ClientToken: "exp-tok-1"}

	first, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	retry, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	if retry.ID != first.ID {
		t.Fatalf("same-token retry id = %s, want original %s", retry.ID, first.ID)
	}

	in.ClientToken = "exp-tok-2"

	other, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	if other.ID == first.ID {
		t.Fatalf("different token must start a new experiment, got %s again", other.ID)
	}

	list, _, err := m.ListExperiments(ctx, driver.Page{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("experiment count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

func TestCreateExperimentTemplateTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, err := m.CreateExperimentTemplate(ctx, sampleCreateInput())
	requireNoError(t, err)

	_, err = m.DeleteExperimentTemplate(ctx, first.ID)
	requireNoError(t, err)

	again, err := m.CreateExperimentTemplate(ctx, sampleCreateInput())
	requireNoError(t, err)

	if again.ID == first.ID {
		t.Fatalf("same-token create after delete replayed the deleted template %s", first.ID)
	}

	_, err = m.GetExperimentTemplate(ctx, again.ID)
	requireNoError(t, err)
}

func TestCreateExperimentTemplateTokenAfterUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, err := m.CreateExperimentTemplate(ctx, sampleCreateInput())
	requireNoError(t, err)

	desc := "updated description"
	_, err = m.UpdateExperimentTemplate(ctx, &driver.UpdateExperimentTemplateInput{ID: first.ID, Description: &desc})
	requireNoError(t, err)

	retry, err := m.CreateExperimentTemplate(ctx, sampleCreateInput())
	requireNoError(t, err)

	if retry.ID != first.ID || retry.Description != desc {
		t.Fatalf("replay = %s %q, want live %s %q", retry.ID, retry.Description, first.ID, desc)
	}
}

func TestStartExperimentTokenReplaysLiveState(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	tpl := mustTemplate(t, m)

	in := &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID, ClientToken: "g1"}

	first, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	_, err = m.StopExperiment(ctx, first.ID)
	requireNoError(t, err)

	live, err := m.GetExperiment(ctx, first.ID)
	requireNoError(t, err)

	retry, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	if retry.ID != first.ID || retry.State.Status != live.State.Status || retry.State.Status != "stopped" {
		t.Fatalf("replay = %s %s, want live %s stopped", retry.ID, retry.State.Status, live.ID)
	}
}

// TestStartExperimentTokenReplaysObservedState: the replay reports the state
// the clock-driven lifecycle has reached (completed), not the stored
// create-time "running".
func TestStartExperimentTokenReplaysObservedState(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(lifecycleStart)
	m := fis.New(config.NewOptions(config.WithClock(fc)))
	tpl := chainedTemplate(t, m)

	in := &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID, ClientToken: "g1"}

	first, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)
	requireStatus(t, first, "running", nil)

	// The chained template runs for three minutes; the token lives for five.
	fc.Advance(4 * time.Minute)

	retry, err := m.StartExperiment(ctx, in)
	requireNoError(t, err)

	if retry.ID != first.ID {
		t.Fatalf("replay id = %s, want %s", retry.ID, first.ID)
	}

	requireStatus(t, retry, "completed", nil)
}

func TestFISClientTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	const n = 20

	tplIDs := make([]string, n)
	expIDs := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			tpl, err := m.CreateExperimentTemplate(ctx, sampleCreateInput())
			if err != nil {
				errs[i] = err

				return
			}

			tplIDs[i] = tpl.ID

			exp, err := m.StartExperiment(ctx, &driver.StartExperimentInput{ExperimentTemplateID: tpl.ID, ClientToken: "burst"})
			if err == nil {
				expIDs[i] = exp.ID
			}

			errs[i] = err
		}()
	}

	close(start)
	wg.Wait()

	for i := range n {
		requireNoError(t, errs[i])

		if tplIDs[i] != tplIDs[0] || expIDs[i] != expIDs[0] {
			t.Fatalf("call %d = %s/%s, want the single %s/%s", i, tplIDs[i], expIDs[i], tplIDs[0], expIDs[0])
		}
	}

	tpls, _, err := m.ListExperimentTemplates(ctx, driver.Page{})
	requireNoError(t, err)

	exps, _, err := m.ListExperiments(ctx, driver.Page{})
	requireNoError(t, err)

	if len(tpls) != 1 || len(exps) != 1 {
		t.Fatalf("templates=%d experiments=%d, want exactly 1 each for one token", len(tpls), len(exps))
	}
}
