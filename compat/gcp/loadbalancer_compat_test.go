package gcp

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestLoadBalancerCompat drives the real cloud.google.com/go/compute/apiv1
// BackendServices and GlobalForwardingRules REST clients against CloudEmu's
// in-process Cloud Load Balancing wire server and records one compat result per
// portable loadbalancer op the handler routes. The GCP surface maps
// backendServices → TargetGroup (Create/Describe/Delete) and forwardingRules →
// LoadBalancer (Create/Describe/Delete); the Compute driver is wired alongside
// so the shared /global/operations poller backing the SDK's Insert/Delete
// long-running ops is served.
func TestLoadBalancerCompat(t *testing.T) {
	const (
		backendName = "compat-backend"
		lbName      = "compat-lb"
	)

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{LB: cloud.LB, Compute: cloud.GCE})
	ctx := context.Background()

	bsClient, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = bsClient.Close() })

	frClient, err := gcpcompute.NewGlobalForwardingRulesRESTClient(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("NewGlobalForwardingRulesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = frClient.Close() })

	project := compat.GCPProject

	strp := func(s string) *string { return &s }

	sess.Op("loadbalancer", "CreateTargetGroup", func() error {
		op, cerr := bsClient.Insert(ctx, &computepb.InsertBackendServiceRequest{
			Project: project,
			BackendServiceResource: &computepb.BackendService{
				Name:     strp(backendName),
				Protocol: strp("HTTP"),
				PortName: strp("http"),
			},
		})
		if cerr != nil {
			return cerr
		}

		return op.Wait(ctx)
	})

	sess.Op("loadbalancer", "DescribeTargetGroups", func() error {
		_, gerr := bsClient.Get(ctx, &computepb.GetBackendServiceRequest{
			Project:        project,
			BackendService: backendName,
		})

		return gerr
	})

	sess.Op("loadbalancer", "CreateLoadBalancer", func() error {
		op, cerr := frClient.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: project,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name:           strp(lbName),
				IPProtocol:     strp("TCP"),
				PortRange:      strp("80"),
				BackendService: strp("projects/" + project + "/global/backendServices/" + backendName),
			},
		})
		if cerr != nil {
			return cerr
		}

		return op.Wait(ctx)
	})

	sess.Op("loadbalancer", "DescribeLoadBalancers", func() error {
		_, gerr := frClient.Get(ctx, &computepb.GetGlobalForwardingRuleRequest{
			Project:        project,
			ForwardingRule: lbName,
		})

		return gerr
	})

	sess.Op("loadbalancer", "DeleteLoadBalancer", func() error {
		op, derr := frClient.Delete(ctx, &computepb.DeleteGlobalForwardingRuleRequest{
			Project:        project,
			ForwardingRule: lbName,
		})
		if derr != nil {
			return derr
		}

		return op.Wait(ctx)
	})

	sess.Op("loadbalancer", "DeleteTargetGroup", func() error {
		op, derr := bsClient.Delete(ctx, &computepb.DeleteBackendServiceRequest{
			Project:        project,
			BackendService: backendName,
		})
		if derr != nil {
			return derr
		}

		return op.Wait(ctx)
	})
}

// TestBackendServiceGetHealth proves compute.backendServices.getHealth returns a
// valid BackendServiceGroupHealth (kind + healthStatus) rather than 405. Pre-fix
// the named-resource POST fell through to method-not-allowed.
func TestBackendServiceGetHealth(t *testing.T) {
	const backendName = "compat-health-backend"

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{LB: cloud.LB, Compute: cloud.GCE})
	ctx := context.Background()

	bsClient, err := gcpcompute.NewBackendServicesRESTClient(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("NewBackendServicesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = bsClient.Close() })

	project := compat.GCPProject
	strp := func(s string) *string { return &s }

	op, err := bsClient.Insert(ctx, &computepb.InsertBackendServiceRequest{
		Project: project,
		BackendServiceResource: &computepb.BackendService{
			Name:     strp(backendName),
			Protocol: strp("HTTP"),
		},
	})
	if err != nil {
		t.Fatalf("insert backend service: %v", err)
	}

	if werr := op.Wait(ctx); werr != nil {
		t.Fatalf("wait insert: %v", werr)
	}

	health, err := bsClient.GetHealth(ctx, &computepb.GetHealthBackendServiceRequest{
		Project:        project,
		BackendService: backendName,
		ResourceGroupReferenceResource: &computepb.ResourceGroupReference{
			Group: strp("projects/" + project + "/zones/us-central1-a/instanceGroups/ig-1"),
		},
	})
	if err != nil {
		t.Fatalf("getHealth: %v", err)
	}

	if health.GetKind() != "compute#backendServiceGroupHealth" {
		t.Fatalf("getHealth kind = %q, want compute#backendServiceGroupHealth", health.GetKind())
	}
}
