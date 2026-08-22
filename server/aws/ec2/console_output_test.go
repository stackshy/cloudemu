package ec2

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	awsec2 "github.com/stackshy/cloudemu/v2/providers/aws/ec2"
)

// recordingEngine is a config.ComputeEngine that records the boot script it was
// handed and returns a fixed console output, letting the wire test assert the
// UserData base64-decode and the GetConsoleOutput round-trip.
type recordingEngine struct {
	lastBoot []byte
	console  []byte
}

func (e *recordingEngine) Provision(
	_ context.Context, req config.ComputeProvisionRequest,
) (config.ComputeProvisionResult, error) {
	e.lastBoot = req.BootScript

	return config.ComputeProvisionResult{IP: "10.0.0.42"}, nil
}

func (e *recordingEngine) ConsoleOutput(_ context.Context, _ string) ([]byte, error) {
	return e.console, nil
}

func (e *recordingEngine) Deprovision(_ context.Context, _ string) error { return nil }

func newEngineHandler(eng config.ComputeEngine) *Handler {
	opts := config.NewOptions(config.WithComputeEngine(eng))

	return New(awsec2.New(opts), nil)
}

func TestRunInstancesBase64DecodesUserData(t *testing.T) {
	eng := &recordingEngine{console: []byte("boot output")}
	h := newEngineHandler(eng)

	script := "#!/bin/sh\necho hello"
	encoded := base64.StdEncoding.EncodeToString([]byte(script))

	run := do(t, h, http.MethodPost, "/", url.Values{
		"Action":       {"RunInstances"},
		"ImageId":      {"ami-x"},
		"InstanceType": {"t2.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
		"UserData":     {encoded},
	})
	if run.Code != http.StatusOK {
		t.Fatalf("RunInstances returned %d: %s", run.Code, run.Body.String())
	}

	if string(eng.lastBoot) != script {
		t.Fatalf("engine boot script = %q, want decoded %q", eng.lastBoot, script)
	}
}

func TestGetConsoleOutputWireAction(t *testing.T) {
	eng := &recordingEngine{console: []byte("boot output")}
	h := newEngineHandler(eng)

	run := do(t, h, http.MethodPost, "/", url.Values{
		"Action":       {"RunInstances"},
		"ImageId":      {"ami-x"},
		"InstanceType": {"t2.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
	})
	id := extractFirstInstanceID(run.Body.String())
	if id == "" {
		t.Fatal("no instance id returned")
	}

	rr := do(t, h, http.MethodPost, "/", url.Values{
		"Action":     {"GetConsoleOutput"},
		"InstanceId": {id},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("GetConsoleOutput returned %d: %s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	wantOutput := base64.StdEncoding.EncodeToString([]byte("boot output"))
	if !strings.Contains(body, "<output>"+wantOutput+"</output>") {
		t.Fatalf("response missing base64 console output %q: %s", wantOutput, body)
	}
	if !strings.Contains(body, "<instanceId>"+id+"</instanceId>") {
		t.Fatalf("response missing instance id: %s", body)
	}
}
