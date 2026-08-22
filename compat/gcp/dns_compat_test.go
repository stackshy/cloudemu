package gcp

import (
	"context"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestDNSCompat drives the real google-cloud dns/v1 client against CloudEmu's
// in-process CloudDNS wire server and records one compat result per portable
// DNS op the handler routes: zone lifecycle plus record create/list/update/
// delete via managed-zone changes.
func TestDNSCompat(t *testing.T) {
	const (
		zoneName = "compat-zone"
		dnsName  = "compat.example.com."
		recName  = "www.compat.example.com."
		recTTL   = 300
		updTTL   = 600
	)

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{CloudDNS: cloud.CloudDNS})
	ctx := context.Background()

	svc, err := dns.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("dns.NewService: %v", err)
	}

	project := compat.GCPProject

	sess.Op("dns", "CreateZone", func() error {
		_, cerr := svc.ManagedZones.Create(project, &dns.ManagedZone{
			Name:        zoneName,
			DnsName:     dnsName,
			Description: "compat zone",
			Visibility:  "public",
			Labels:      map[string]string{"env": "compat"},
		}).Context(ctx).Do()

		return cerr
	})

	sess.Op("dns", "GetZone", func() error {
		_, gerr := svc.ManagedZones.Get(project, zoneName).Context(ctx).Do()

		return gerr
	})

	sess.Op("dns", "ListZones", func() error {
		_, lerr := svc.ManagedZones.List(project).Context(ctx).Do()

		return lerr
	})

	rec := &dns.ResourceRecordSet{
		Name:    recName,
		Type:    "A",
		Ttl:     recTTL,
		Rrdatas: []string{"192.0.2.1"},
	}

	sess.Op("dns", "CreateRecord", func() error {
		_, cerr := svc.Changes.Create(project, zoneName, &dns.Change{
			Additions: []*dns.ResourceRecordSet{rec},
		}).Context(ctx).Do()

		return cerr
	})

	sess.Op("dns", "ListRecords", func() error {
		_, lerr := svc.ResourceRecordSets.List(project, zoneName).Context(ctx).Do()

		return lerr
	})

	updated := &dns.ResourceRecordSet{
		Name:    recName,
		Type:    "A",
		Ttl:     updTTL,
		Rrdatas: []string{"192.0.2.2"},
	}

	sess.Op("dns", "UpdateRecord", func() error {
		_, uerr := svc.Changes.Create(project, zoneName, &dns.Change{
			Deletions: []*dns.ResourceRecordSet{rec},
			Additions: []*dns.ResourceRecordSet{updated},
		}).Context(ctx).Do()

		return uerr
	})

	sess.Op("dns", "DeleteRecord", func() error {
		_, derr := svc.Changes.Create(project, zoneName, &dns.Change{
			Deletions: []*dns.ResourceRecordSet{updated},
		}).Context(ctx).Do()

		return derr
	})

	sess.Op("dns", "DeleteZone", func() error {
		return svc.ManagedZones.Delete(project, zoneName).Context(ctx).Do()
	})
}
