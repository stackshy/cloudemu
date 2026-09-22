package awsevents_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
)

type recordingPublisher struct {
	calls []string
}

func (r *recordingPublisher) PublishServiceEvent(_ context.Context, source, detailType string, _ any, resources []string) {
	r.calls = append(r.calls, source+"|"+detailType+"|"+strings.Join(resources, ","))
}

func TestEmitterUnwiredIsNoOp(t *testing.T) {
	var e awsevents.Emitter
	e.Emit(context.Background(), "aws.ec2", "x", nil)

	var nilEmitter *awsevents.Emitter
	nilEmitter.Emit(context.Background(), "aws.ec2", "x", nil)
}

func TestEmitterPublishesWhenWired(t *testing.T) {
	var e awsevents.Emitter

	rec := &recordingPublisher{}
	e.SetPublisher(rec)
	e.Emit(context.Background(), "aws.ssm", "Parameter Store Change", map[string]string{}, "arn:a", "arn:b")

	if len(rec.calls) != 1 || rec.calls[0] != "aws.ssm|Parameter Store Change|arn:a,arn:b" {
		t.Fatalf("calls = %v", rec.calls)
	}

	e.SetPublisher(nil)
	e.Emit(context.Background(), "aws.ssm", "Parameter Store Change", nil)

	if len(rec.calls) != 1 {
		t.Fatalf("emit after SetPublisher(nil) published: %v", rec.calls)
	}
}
