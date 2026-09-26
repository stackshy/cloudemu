package clouddns_test

import (
	"context"
	"errors"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKChangesCreateRejectsBadAddress checks that an A or AAAA rrdata that
// is not an address of the right family fails the change with 400 invalid.
// The change is atomic, so the good addition and the deletion in the same
// batch must not apply either.
func TestSDKChangesCreateRejectsBadAddress(t *testing.T) {
	tests := []struct {
		name    string
		rtype   string
		rrdatas []string
		wantMsg string
	}{
		{
			name: "A not an IP", rtype: "A", rrdatas: []string{"192.0.2.9", "999.1.1.1"},
			wantMsg: "Invalid value for 'entity.change.additions[1].rrdata[1]': '999.1.1.1'",
		},
		{
			name: "A holding IPv6", rtype: "A", rrdatas: []string{"2001:db8::1"},
			wantMsg: "Invalid value for 'entity.change.additions[1].rrdata[0]': '2001:db8::1'",
		},
		{
			name: "AAAA holding IPv4", rtype: "AAAA", rrdatas: []string{"192.0.2.1"},
			wantMsg: "Invalid value for 'entity.change.additions[1].rrdata[0]': '192.0.2.1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newDNSService(t)
			ctx := context.Background()

			createZone(t, svc, &dns.ManagedZone{Name: "ip-zone", DnsName: "ip.example.com."})

			old := &dns.ResourceRecordSet{Name: "old.ip.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}}
			if err := addRecord(t, svc, "ip-zone", old); err != nil {
				t.Fatalf("seed addRecord: %v", err)
			}

			_, err := svc.Changes.Create(testProject, "ip-zone", &dns.Change{
				Deletions: []*dns.ResourceRecordSet{old},
				Additions: []*dns.ResourceRecordSet{
					{Name: "good.ip.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.2"}},
					{Name: "bad.ip.example.com.", Type: tt.rtype, Ttl: 300, Rrdatas: tt.rrdatas},
				},
			}).Context(ctx).Do()

			var gerr *googleapi.Error
			if !errors.As(err, &gerr) || gerr.Code != 400 {
				t.Fatalf("got %v, want 400", err)
			}

			if gerr.Message != tt.wantMsg || len(gerr.Errors) == 0 || gerr.Errors[0].Reason != "invalid" {
				t.Fatalf("got message %q reasons %+v, want %q with reason invalid", gerr.Message, gerr.Errors, tt.wantMsg)
			}

			if findRecord(t, svc, ctx, "ip-zone", "old.ip.example.com.") == nil {
				t.Fatal("deletion applied although the change was rejected")
			}

			if rec := findRecord(t, svc, ctx, "ip-zone", "good.ip.example.com."); rec != nil {
				t.Fatalf("good addition applied although the change was rejected: %+v", rec)
			}
		})
	}
}

// TestSDKChangesCreateAcceptsGoodAddresses checks that valid A and AAAA
// rrdatas still apply.
func TestSDKChangesCreateAcceptsGoodAddresses(t *testing.T) {
	svc := newDNSService(t)

	createZone(t, svc, &dns.ManagedZone{Name: "okip-zone", DnsName: "okip.example.com."})

	_, err := svc.Changes.Create(testProject, "okip-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{Name: "v4.okip.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1", "198.51.100.1"}},
			{Name: "v6.okip.example.com.", Type: "AAAA", Ttl: 300, Rrdatas: []string{"2001:db8::1"}},
		},
	}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Changes.Create: %v", err)
	}
}
