package fis_test

import (
	"context"
	"testing"

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
