package loadbalancer_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

const (
	saProducerRule = "projects/" + testProject + "/regions/" + testRegion + "/forwardingRules/producer-ilb"
	saNatSubnet    = "projects/" + testProject + "/regions/" + testRegion + "/subnetworks/psc-nat"
)

func newServiceAttachmentsClient(t *testing.T, ts *httptest.Server) *gcpcompute.ServiceAttachmentsClient {
	t.Helper()

	c, err := gcpcompute.NewServiceAttachmentsRESTClient(context.Background(), clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewServiceAttachmentsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// attachment builds a valid service attachment named name.
func attachment(name, preference string) *computepb.ServiceAttachment {
	return &computepb.ServiceAttachment{
		Name:                 ptrStr(name),
		TargetService:        ptrStr(saProducerRule),
		NatSubnets:           []string{saNatSubnet},
		ConnectionPreference: ptrStr(preference),
	}
}

// insertAttachment inserts sa in testRegion, failing the test on error.
func insertAttachment(ctx context.Context, t *testing.T, ts *httptest.Server, sa *computepb.ServiceAttachment) {
	t.Helper()

	c := newServiceAttachmentsClient(t, ts)

	waitOp(ctx, t, "ServiceAttachment Insert "+sa.GetName(), func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertServiceAttachmentRequest{
			Project: testProject, Region: testRegion, ServiceAttachmentResource: sa,
		})
	})
}

func getAttachment(ctx context.Context, t *testing.T, c *gcpcompute.ServiceAttachmentsClient, name string) *computepb.ServiceAttachment {
	t.Helper()

	got, err := c.Get(ctx, &computepb.GetServiceAttachmentRequest{Project: testProject, Region: testRegion, ServiceAttachment: name})
	if err != nil {
		t.Fatalf("ServiceAttachment Get %s: %v", name, err)
	}

	return got
}

// TestSDKGCPServiceAttachmentLifecycle drives compute.serviceAttachments
// (501 before) through the real ServiceAttachmentsClient: insert, get, list,
// patch, delete, and the 400/404/409 refusals.
func TestSDKGCPServiceAttachmentLifecycle(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	c := newServiceAttachmentsClient(t, ts)

	sa := attachment("producer-sa", "ACCEPT_MANUAL")
	sa.ConsumerAcceptLists = []*computepb.ServiceAttachmentConsumerProjectLimit{
		{ProjectIdOrNum: ptrStr("consumer-a"), ConnectionLimit: func() *uint32 { v := uint32(5); return &v }()},
	}
	sa.ConsumerRejectLists = []string{"consumer-bad"}
	sa.EnableProxyProtocol = ptrBool(true)
	insertAttachment(ctx, t, ts, sa)

	got := getAttachment(ctx, t, c, "producer-sa")
	if got.GetKind() != "compute#serviceAttachment" || got.GetId() == 0 || got.GetTargetService() != saProducerRule ||
		got.GetConnectionPreference() != "ACCEPT_MANUAL" || len(got.GetNatSubnets()) != 1 || !got.GetEnableProxyProtocol() ||
		len(got.GetConsumerAcceptLists()) != 1 || got.GetConsumerAcceptLists()[0].GetConnectionLimit() != 5 ||
		len(got.GetConsumerRejectLists()) != 1 || got.GetRegion() == "" || got.GetSelfLink() == "" {
		t.Fatalf("Get = %+v, want the inserted attachment", got)
	}

	insert := func(sa *computepb.ServiceAttachment) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.Insert(ctx, &computepb.InsertServiceAttachmentRequest{
				Project: testProject, Region: testRegion, ServiceAttachmentResource: sa,
			})
		})
	}

	assertHTTPCode(t, insert(attachment("producer-sa", "ACCEPT_AUTOMATIC")), 409)
	assertHTTPCode(t, insert(attachment("Bad_Name", "ACCEPT_AUTOMATIC")), 400)
	assertHTTPCode(t, insert(attachment("bad-pref", "ACCEPT_SOMETIMES")), 400)

	noNat := attachment("no-nat", "ACCEPT_AUTOMATIC")
	noNat.NatSubnets = nil
	assertHTTPCode(t, insert(noNat), 400)

	noTarget := attachment("no-target", "ACCEPT_AUTOMATIC")
	noTarget.TargetService = nil
	assertHTTPCode(t, insert(noTarget), 400)

	emptyEntry := attachment("empty-entry", "ACCEPT_MANUAL")
	emptyEntry.ConsumerAcceptLists = []*computepb.ServiceAttachmentConsumerProjectLimit{{}}
	assertHTTPCode(t, insert(emptyEntry), 400)

	insertAttachment(ctx, t, ts, attachment("second-sa", "ACCEPT_AUTOMATIC"))

	it := c.List(ctx, &computepb.ListServiceAttachmentsRequest{Project: testProject, Region: testRegion})

	var names []string

	for {
		sa, err := it.Next()
		if err != nil {
			break
		}

		names = append(names, sa.GetName())
	}

	if len(names) != 2 || names[0] != "producer-sa" || names[1] != "second-sa" {
		t.Fatalf("List = %v, want [producer-sa second-sa]", names)
	}

	patch := func(name string, sa *computepb.ServiceAttachment) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.Patch(ctx, &computepb.PatchServiceAttachmentRequest{
				Project: testProject, Region: testRegion, ServiceAttachment: name, ServiceAttachmentResource: sa,
			})
		})
	}

	if err := patch("producer-sa", &computepb.ServiceAttachment{ConnectionPreference: ptrStr("ACCEPT_AUTOMATIC")}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if got := getAttachment(ctx, t, c, "producer-sa"); got.GetConnectionPreference() != "ACCEPT_AUTOMATIC" ||
		got.GetTargetService() != saProducerRule {
		t.Fatalf("after Patch: preference=%q targetService=%q", got.GetConnectionPreference(), got.GetTargetService())
	}

	assertHTTPCode(t, patch("producer-sa", &computepb.ServiceAttachment{ConnectionPreference: ptrStr("NOPE")}), 400)
	assertHTTPCode(t, patch("ghost-sa", &computepb.ServiceAttachment{Description: ptrStr("x")}), 404)

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Delete(ctx, &computepb.DeleteServiceAttachmentRequest{Project: testProject, Region: testRegion, ServiceAttachment: "second-sa"})
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := c.Get(ctx, &computepb.GetServiceAttachmentRequest{Project: testProject, Region: testRegion, ServiceAttachment: "second-sa"})
	assertHTTPCode(t, err, 404)
}

// TestSDKGCPPSCStatusFromServiceAttachment: a consumer rule's
// pscConnectionStatus comes from the attachment it targets. Under
// ACCEPT_MANUAL an accept-listed project is ACCEPTED up to its
// connectionLimit and PENDING beyond it, a reject-listed one is REJECTED, an
// unlisted one is PENDING; statuses re-evaluate when the lists change, the
// attachment lists its connectedEndpoints, and deleting the attachment leaves
// its consumers CLOSED.
func TestSDKGCPPSCStatusFromServiceAttachment(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	sac := newServiceAttachmentsClient(t, ts)
	frc := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	one := uint32(1)
	sa := attachment("manual-sa", "ACCEPT_MANUAL")
	sa.ConsumerAcceptLists = []*computepb.ServiceAttachmentConsumerProjectLimit{
		{ProjectIdOrNum: ptrStr(testProject), ConnectionLimit: &one},
	}
	insertAttachment(ctx, t, ts, sa)
	insertAttachment(ctx, t, ts, attachment("unlisted-sa", "ACCEPT_MANUAL"))

	target := "projects/" + testProject + "/regions/" + testRegion + "/serviceAttachments/"

	connect := func(name, attachment string) {
		t.Helper()

		waitOp(ctx, t, "ForwardingRule Insert "+name, func() (*gcpcompute.Operation, error) {
			return frc.Insert(ctx, &computepb.InsertForwardingRuleRequest{
				Project: testProject, Region: testRegion,
				ForwardingRuleResource: &computepb.ForwardingRule{
					Name: ptrStr(name), Network: ptrStr(pscNetwork), Target: ptrStr(target + attachment),
				},
			})
		})
	}

	status := func(name string) string {
		t.Helper()

		got, err := frc.Get(ctx, &computepb.GetForwardingRuleRequest{Project: testProject, Region: testRegion, ForwardingRule: name})
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}

		return got.GetPscConnectionStatus()
	}

	want := func(what string, pairs ...string) {
		t.Helper()

		for i := 0; i < len(pairs); i += 2 {
			if got := status(pairs[i]); got != pairs[i+1] {
				t.Errorf("%s: %s pscConnectionStatus = %q, want %s", what, pairs[i], got, pairs[i+1])
			}
		}
	}

	connect("ep-1", "manual-sa")
	connect("ep-2", "manual-sa")
	connect("ep-unlisted", "unlisted-sa")
	want("limit 1", "ep-1", "ACCEPTED", "ep-2", "PENDING", "ep-unlisted", "PENDING")

	if eps := getAttachment(ctx, t, sac, "manual-sa").GetConnectedEndpoints(); len(eps) != 2 ||
		eps[0].GetStatus() != "ACCEPTED" || eps[1].GetStatus() != "PENDING" || eps[0].GetPscConnectionId() == 0 ||
		eps[0].GetConsumerNetwork() != pscNetwork {
		t.Fatalf("connectedEndpoints = %v, want ep-1 ACCEPTED then ep-2 PENDING", eps)
	}

	patch := func(sa *computepb.ServiceAttachment) {
		t.Helper()

		waitOp(ctx, t, "ServiceAttachment Patch", func() (*gcpcompute.Operation, error) {
			return sac.Patch(ctx, &computepb.PatchServiceAttachmentRequest{
				Project: testProject, Region: testRegion, ServiceAttachment: "manual-sa", ServiceAttachmentResource: sa,
			})
		})
	}

	two := uint32(2)
	patch(&computepb.ServiceAttachment{ConsumerAcceptLists: []*computepb.ServiceAttachmentConsumerProjectLimit{
		{ProjectIdOrNum: ptrStr(testProject), ConnectionLimit: &two},
	}})
	want("limit raised to 2", "ep-1", "ACCEPTED", "ep-2", "ACCEPTED")

	patch(&computepb.ServiceAttachment{ConsumerRejectLists: []string{testProject}})
	want("project rejected", "ep-1", "REJECTED", "ep-2", "REJECTED")

	waitOp(ctx, t, "ForwardingRule Delete", func() (*gcpcompute.Operation, error) {
		return frc.Delete(ctx, &computepb.DeleteForwardingRuleRequest{Project: testProject, Region: testRegion, ForwardingRule: "ep-2"})
	})

	if eps := getAttachment(ctx, t, sac, "manual-sa").GetConnectedEndpoints(); len(eps) != 1 {
		t.Fatalf("connectedEndpoints after deleting ep-2 = %v, want 1", eps)
	}

	waitOp(ctx, t, "ServiceAttachment Delete", func() (*gcpcompute.Operation, error) {
		return sac.Delete(ctx, &computepb.DeleteServiceAttachmentRequest{Project: testProject, Region: testRegion, ServiceAttachment: "manual-sa"})
	})
	want("attachment deleted", "ep-1", "CLOSED")
}

// TestSDKGCPPSCAttachmentTargetMustExist: GCP refuses a PSC endpoint for a
// service attachment it cannot find, or one in another region.
func TestSDKGCPPSCAttachmentTargetMustExist(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	frc := newRegionalForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insertAttachment(ctx, t, ts, attachment("real-sa", "ACCEPT_AUTOMATIC"))

	insert := func(target string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return frc.Insert(ctx, &computepb.InsertForwardingRuleRequest{
				Project: testProject, Region: testRegion,
				ForwardingRuleResource: &computepb.ForwardingRule{
					Name: ptrStr("ep"), Network: ptrStr(pscNetwork), Target: ptrStr(target),
				},
			})
		})
	}

	wantBadRequest(t, insert("projects/"+testProject+"/regions/"+testRegion+"/serviceAttachments/nosuch"), "target")
	wantBadRequest(t, insert("projects/"+testProject+"/regions/europe-west1/serviceAttachments/real-sa"), "target")

	if err := insert("projects/" + testProject + "/regions/" + testRegion + "/serviceAttachments/real-sa"); err != nil {
		t.Fatalf("Insert against an existing attachment: %v", err)
	}
}
