package bedrockagent

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// primaryOnly hides every optional capability of the wrapped driver.
type primaryOnly struct{ driver.BedrockAgent }

func TestServiceCapabilitiesDelegate(t *testing.T) {
	rec := recorder.New()
	svc := newService()
	svc.recorder = rec
	ctx := context.Background()

	agent, err := svc.CreateAgent(ctx, driver.AgentConfig{Name: "cap"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	alias, err := svc.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: agent.ID, Name: "prod"})
	if err != nil {
		t.Fatalf("CreateAgentAlias: %v", err)
	}

	got, err := svc.GetAgentAlias(ctx, agent.ID, alias.ID)
	if err != nil || got.ID != alias.ID {
		t.Fatalf("GetAgentAlias: %v %+v", err, got)
	}

	versions, _, err := svc.ListAgentVersions(ctx, agent.ID, driver.Page{})
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListAgentVersions: %v %d", err, len(versions))
	}

	if err := svc.DeleteAgentActionGroup(ctx, agent.ID, driver.DraftVersion, "MISSING000"); !cerrors.IsNotFound(err) {
		t.Fatalf("DeleteAgentActionGroup: want not found, got %v", err)
	}

	if n := len(rec.CallsFor(service, "GetAgentAlias")); n != 1 {
		t.Fatalf("recorded %d GetAgentAlias calls, want 1", n)
	}
}

func TestServiceCapabilityMissing(t *testing.T) {
	svc := NewBedrockAgent(primaryOnly{newService().driver})

	_, err := svc.GetAgentVersion(context.Background(), "ABCDEFGHIJ", "1")
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	if err := svc.DeletePromptVersion(context.Background(), "ABCDEFGHIJ", "1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestServiceCapabilityErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	boom := errors.New("injected")
	inj.Set(service, "ValidateFlowDefinition", boom, inject.Always{})

	svc := newService()
	svc.injector = inj

	if _, err := svc.ValidateFlowDefinition(context.Background(), []byte(`{}`)); !errors.Is(err, boom) {
		t.Fatalf("want injected error, got %v", err)
	}
}

// TestServiceEveryCapabilityMethod calls each capability method through the
// pipeline: against the provider it reaches the driver (and fails on the
// missing ids or empty input it is given), against a primary-only driver it
// fails first with FailedPrecondition.
func TestServiceEveryCapabilityMethod(t *testing.T) {
	ctx := context.Background()
	full := newService()
	bare := NewBedrockAgent(primaryOnly{full.driver})

	calls := map[string]func(*BedrockAgent) error{
		"GetAgentVersion": func(svc *BedrockAgent) error {
			_, err := svc.GetAgentVersion(ctx, "MISSING000", "MISSING000")

			return err
		},
		"ListAgentVersions": func(svc *BedrockAgent) error {
			_, _, err := svc.ListAgentVersions(ctx, "MISSING000", driver.Page{})

			return err
		},
		"DeleteAgentVersion": func(svc *BedrockAgent) error {
			_, err := svc.DeleteAgentVersion(ctx, "MISSING000", "MISSING000", false)

			return err
		},
		"GetAgentAlias": func(svc *BedrockAgent) error {
			_, err := svc.GetAgentAlias(ctx, "MISSING000", "MISSING000")

			return err
		},
		"ListAgentAliases": func(svc *BedrockAgent) error {
			_, _, err := svc.ListAgentAliases(ctx, "MISSING000", driver.Page{})

			return err
		},
		"UpdateAgentAlias": func(svc *BedrockAgent) error {
			_, err := svc.UpdateAgentAlias(ctx, "MISSING000", driver.AgentAliasConfig{})

			return err
		},
		"DeleteAgentAlias": func(svc *BedrockAgent) error {
			_, err := svc.DeleteAgentAlias(ctx, "MISSING000", "MISSING000")

			return err
		},
		"CreateAgentActionGroup": func(svc *BedrockAgent) error {
			_, err := svc.CreateAgentActionGroup(ctx, driver.ActionGroupConfig{})

			return err
		},
		"GetAgentActionGroup": func(svc *BedrockAgent) error {
			_, err := svc.GetAgentActionGroup(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"UpdateAgentActionGroup": func(svc *BedrockAgent) error {
			_, err := svc.UpdateAgentActionGroup(ctx, "MISSING000", driver.ActionGroupConfig{})

			return err
		},
		"DeleteAgentActionGroup": func(svc *BedrockAgent) error {
			return svc.DeleteAgentActionGroup(ctx, "MISSING000", "MISSING000", "MISSING000")
		},
		"ListAgentActionGroups": func(svc *BedrockAgent) error {
			_, _, err := svc.ListAgentActionGroups(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"AssociateAgentKnowledgeBase": func(svc *BedrockAgent) error {
			_, err := svc.AssociateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{})

			return err
		},
		"GetAgentKnowledgeBase": func(svc *BedrockAgent) error {
			_, err := svc.GetAgentKnowledgeBase(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"UpdateAgentKnowledgeBase": func(svc *BedrockAgent) error {
			_, err := svc.UpdateAgentKnowledgeBase(ctx, driver.AgentKnowledgeBaseConfig{})

			return err
		},
		"DisassociateAgentKnowledgeBase": func(svc *BedrockAgent) error {
			return svc.DisassociateAgentKnowledgeBase(ctx, "MISSING000", "MISSING000", "MISSING000")
		},
		"ListAgentKnowledgeBases": func(svc *BedrockAgent) error {
			_, _, err := svc.ListAgentKnowledgeBases(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"AssociateAgentCollaborator": func(svc *BedrockAgent) error {
			_, err := svc.AssociateAgentCollaborator(ctx, driver.AgentCollaboratorConfig{})

			return err
		},
		"GetAgentCollaborator": func(svc *BedrockAgent) error {
			_, err := svc.GetAgentCollaborator(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"UpdateAgentCollaborator": func(svc *BedrockAgent) error {
			_, err := svc.UpdateAgentCollaborator(ctx, "MISSING000", driver.AgentCollaboratorConfig{})

			return err
		},
		"DisassociateAgentCollaborator": func(svc *BedrockAgent) error {
			return svc.DisassociateAgentCollaborator(ctx, "MISSING000", "MISSING000", "MISSING000")
		},
		"ListAgentCollaborators": func(svc *BedrockAgent) error {
			_, _, err := svc.ListAgentCollaborators(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"GetIngestionJob": func(svc *BedrockAgent) error {
			_, err := svc.GetIngestionJob(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"ListIngestionJobs": func(svc *BedrockAgent) error {
			_, _, err := svc.ListIngestionJobs(ctx, driver.ListIngestionJobsInput{})

			return err
		},
		"StopIngestionJob": func(svc *BedrockAgent) error {
			_, err := svc.StopIngestionJob(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"CreateFlowVersion": func(svc *BedrockAgent) error {
			_, err := svc.CreateFlowVersion(ctx, "MISSING000", "MISSING000")

			return err
		},
		"GetFlowVersion": func(svc *BedrockAgent) error {
			_, err := svc.GetFlowVersion(ctx, "MISSING000", "MISSING000")

			return err
		},
		"ListFlowVersions": func(svc *BedrockAgent) error {
			_, _, err := svc.ListFlowVersions(ctx, "MISSING000", driver.Page{})

			return err
		},
		"DeleteFlowVersion": func(svc *BedrockAgent) error {
			return svc.DeleteFlowVersion(ctx, "MISSING000", "MISSING000", false)
		},
		"CreateFlowAlias": func(svc *BedrockAgent) error {
			_, err := svc.CreateFlowAlias(ctx, driver.FlowAliasConfig{})

			return err
		},
		"GetFlowAlias": func(svc *BedrockAgent) error {
			_, err := svc.GetFlowAlias(ctx, "MISSING000", "MISSING000")

			return err
		},
		"ListFlowAliases": func(svc *BedrockAgent) error {
			_, _, err := svc.ListFlowAliases(ctx, "MISSING000", driver.Page{})

			return err
		},
		"UpdateFlowAlias": func(svc *BedrockAgent) error {
			_, err := svc.UpdateFlowAlias(ctx, "MISSING000", driver.FlowAliasConfig{})

			return err
		},
		"DeleteFlowAlias": func(svc *BedrockAgent) error {
			_, err := svc.DeleteFlowAlias(ctx, "MISSING000", "MISSING000")

			return err
		},
		"CreatePromptVersion": func(svc *BedrockAgent) error {
			_, err := svc.CreatePromptVersion(ctx, "MISSING000", "MISSING000", nil)

			return err
		},
		"GetPromptVersion": func(svc *BedrockAgent) error {
			_, err := svc.GetPromptVersion(ctx, "MISSING000", "MISSING000")

			return err
		},
		"ListPromptVersions": func(svc *BedrockAgent) error {
			_, _, err := svc.ListPromptVersions(ctx, "MISSING000", driver.Page{})

			return err
		},
		"DeletePromptVersion": func(svc *BedrockAgent) error {
			return svc.DeletePromptVersion(ctx, "MISSING000", "MISSING000")
		},
		"IngestKnowledgeBaseDocuments": func(svc *BedrockAgent) error {
			_, err := svc.IngestKnowledgeBaseDocuments(ctx, "MISSING000", "MISSING000", nil)

			return err
		},
		"GetKnowledgeBaseDocuments": func(svc *BedrockAgent) error {
			_, err := svc.GetKnowledgeBaseDocuments(ctx, "MISSING000", "MISSING000", nil)

			return err
		},
		"ListKnowledgeBaseDocuments": func(svc *BedrockAgent) error {
			_, _, err := svc.ListKnowledgeBaseDocuments(ctx, "MISSING000", "MISSING000", driver.Page{})

			return err
		},
		"DeleteKnowledgeBaseDocuments": func(svc *BedrockAgent) error {
			_, err := svc.DeleteKnowledgeBaseDocuments(ctx, "MISSING000", "MISSING000", nil)

			return err
		},
		"ValidateFlowDefinition": func(svc *BedrockAgent) error {
			_, err := svc.ValidateFlowDefinition(ctx, []byte(`{}`))

			return err
		},
		"PutResourcePolicy": func(svc *BedrockAgent) error {
			_, err := svc.PutResourcePolicy(ctx, "MISSING000", "MISSING000", "MISSING000")

			return err
		},
		"GetResourcePolicy": func(svc *BedrockAgent) error {
			_, err := svc.GetResourcePolicy(ctx, "MISSING000")

			return err
		},
		"DeleteResourcePolicy": func(svc *BedrockAgent) error {
			_, err := svc.DeleteResourcePolicy(ctx, "MISSING000", "MISSING000")

			return err
		},
	}

	for name, call := range calls {
		if err := call(full); cerrors.IsFailedPrecondition(err) {
			t.Errorf("%s: the provider implements it, got %v", name, err)
		}

		if err := call(bare); !cerrors.IsFailedPrecondition(err) {
			t.Errorf("%s: want FailedPrecondition without the capability, got %v", name, err)
		}
	}
}
