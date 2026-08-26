package route53_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsr53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// createPrivateZone creates a private hosted zone associated with one VPC and
// returns its id.
func createPrivateZone(t *testing.T, client *awsr53.Client, name, ref, vpcID string) string {
	t.Helper()

	out, err := client.CreateHostedZone(context.Background(), &awsr53.CreateHostedZoneInput{
		Name:            aws.String(name),
		CallerReference: aws.String(ref),
		VPC: &r53types.VPC{
			VPCId:     aws.String(vpcID),
			VPCRegion: r53types.VPCRegionUsEast1,
		},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone(%s): %v", name, err)
	}

	return aws.ToString(out.HostedZone.Id)
}

// TestSDKCreateHostedZoneWithVPC proves a zone created with a VPC is private,
// echoes the VPC on create, and GetHostedZone returns PrivateZone + the VPC.
func TestSDKCreateHostedZoneWithVPC(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	out, err := client.CreateHostedZone(ctx, &awsr53.CreateHostedZoneInput{
		Name:            aws.String("private.internal."),
		CallerReference: aws.String("pz-ref"),
		VPC: &r53types.VPC{
			VPCId:     aws.String("vpc-aaaa1111"),
			VPCRegion: r53types.VPCRegionUsEast1,
		},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}

	if out.HostedZone.Config == nil || !out.HostedZone.Config.PrivateZone {
		t.Fatalf("Config = %+v, want PrivateZone=true", out.HostedZone.Config)
	}

	if out.VPC == nil || aws.ToString(out.VPC.VPCId) != "vpc-aaaa1111" {
		t.Fatalf("create VPC = %+v, want vpc-aaaa1111", out.VPC)
	}

	id := aws.ToString(out.HostedZone.Id)

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if got.HostedZone.Config == nil || !got.HostedZone.Config.PrivateZone {
		t.Errorf("GetHostedZone Config = %+v, want PrivateZone=true", got.HostedZone.Config)
	}

	if len(got.VPCs) != 1 || aws.ToString(got.VPCs[0].VPCId) != "vpc-aaaa1111" {
		t.Fatalf("GetHostedZone VPCs = %+v, want [vpc-aaaa1111]", got.VPCs)
	}

	if got.VPCs[0].VPCRegion != r53types.VPCRegionUsEast1 {
		t.Errorf("VPCRegion = %q, want us-east-1", got.VPCs[0].VPCRegion)
	}
}

// TestSDKAssociateVPCWithHostedZone proves a second VPC can be associated and
// GetHostedZone then lists both, with a ChangeInfo returned.
func TestSDKAssociateVPCWithHostedZone(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createPrivateZone(t, client, "assoc.internal.", "assoc-ref", "vpc-1111")

	assoc, err := client.AssociateVPCWithHostedZone(ctx, &awsr53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC: &r53types.VPC{
			VPCId:     aws.String("vpc-2222"),
			VPCRegion: r53types.VPCRegionUsEast1,
		},
		Comment: aws.String("add second vpc"),
	})
	if err != nil {
		t.Fatalf("AssociateVPCWithHostedZone: %v", err)
	}

	if assoc.ChangeInfo == nil || assoc.ChangeInfo.Status != r53types.ChangeStatusInsync {
		t.Fatalf("ChangeInfo = %+v, want INSYNC", assoc.ChangeInfo)
	}

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	ids := map[string]bool{}
	for _, v := range got.VPCs {
		ids[aws.ToString(v.VPCId)] = true
	}

	if len(got.VPCs) != 2 || !ids["vpc-1111"] || !ids["vpc-2222"] {
		t.Fatalf("GetHostedZone VPCs = %+v, want both vpc-1111 and vpc-2222", got.VPCs)
	}
}

// TestSDKAssociateVPCIdempotent proves re-associating an already-associated VPC
// succeeds without duplicating it.
func TestSDKAssociateVPCIdempotent(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createPrivateZone(t, client, "idem.internal.", "idem-ref", "vpc-1111")

	if _, err := client.AssociateVPCWithHostedZone(ctx, &awsr53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-1111"), VPCRegion: r53types.VPCRegionUsEast1},
	}); err != nil {
		t.Fatalf("AssociateVPCWithHostedZone (idempotent): %v", err)
	}

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if len(got.VPCs) != 1 {
		t.Fatalf("GetHostedZone VPCs = %+v, want exactly one (no duplicate)", got.VPCs)
	}
}

// TestSDKDisassociateVPCFromHostedZone proves a VPC can be removed while the
// others remain.
func TestSDKDisassociateVPCFromHostedZone(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createPrivateZone(t, client, "disassoc.internal.", "dis-ref", "vpc-1111")

	if _, err := client.AssociateVPCWithHostedZone(ctx, &awsr53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-2222"), VPCRegion: r53types.VPCRegionUsEast1},
	}); err != nil {
		t.Fatalf("AssociateVPCWithHostedZone: %v", err)
	}

	dis, err := client.DisassociateVPCFromHostedZone(ctx, &awsr53.DisassociateVPCFromHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-1111"), VPCRegion: r53types.VPCRegionUsEast1},
	})
	if err != nil {
		t.Fatalf("DisassociateVPCFromHostedZone: %v", err)
	}

	if dis.ChangeInfo == nil || dis.ChangeInfo.Status != r53types.ChangeStatusInsync {
		t.Fatalf("ChangeInfo = %+v, want INSYNC", dis.ChangeInfo)
	}

	got, err := client.GetHostedZone(ctx, &awsr53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}

	if len(got.VPCs) != 1 || aws.ToString(got.VPCs[0].VPCId) != "vpc-2222" {
		t.Fatalf("GetHostedZone VPCs = %+v, want only vpc-2222", got.VPCs)
	}
}

// TestSDKDisassociateLastVPC proves removing the only VPC is rejected with
// LastVPCAssociation, matching real Route 53.
func TestSDKDisassociateLastVPC(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createPrivateZone(t, client, "last.internal.", "last-ref", "vpc-1111")

	_, err := client.DisassociateVPCFromHostedZone(ctx, &awsr53.DisassociateVPCFromHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-1111"), VPCRegion: r53types.VPCRegionUsEast1},
	})
	if err == nil {
		t.Fatal("DisassociateVPCFromHostedZone(last VPC) succeeded, want LastVPCAssociation error")
	}

	var last *r53types.LastVPCAssociation
	if !errors.As(err, &last) {
		t.Fatalf("error = %v, want *LastVPCAssociation", err)
	}
}

// TestSDKDisassociateVPCNotAssociated proves removing a VPC that isn't
// associated is rejected with VPCAssociationNotFound.
func TestSDKDisassociateVPCNotAssociated(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createPrivateZone(t, client, "notassoc.internal.", "na-ref", "vpc-1111")

	_, err := client.DisassociateVPCFromHostedZone(ctx, &awsr53.DisassociateVPCFromHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-9999"), VPCRegion: r53types.VPCRegionUsEast1},
	})
	if err == nil {
		t.Fatal("DisassociateVPCFromHostedZone(unassociated VPC) succeeded, want VPCAssociationNotFound")
	}

	var notFound *r53types.VPCAssociationNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want *VPCAssociationNotFound", err)
	}
}

// TestSDKAssociateVPCOnPublicZone proves associating a VPC with a public hosted
// zone is rejected with PublicZoneVPCAssociation.
func TestSDKAssociateVPCOnPublicZone(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	id := createZone(t, client, "public.example.", "pub-ref")

	_, err := client.AssociateVPCWithHostedZone(ctx, &awsr53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String(id),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-1111"), VPCRegion: r53types.VPCRegionUsEast1},
	})
	if err == nil {
		t.Fatal("AssociateVPCWithHostedZone(public zone) succeeded, want PublicZoneVPCAssociation")
	}

	var pub *r53types.PublicZoneVPCAssociation
	if !errors.As(err, &pub) {
		t.Fatalf("error = %v, want *PublicZoneVPCAssociation", err)
	}
}

// TestSDKAssociateVPCMissingZone proves associating a VPC with a hosted zone
// that doesn't exist is rejected with NoSuchHostedZone.
func TestSDKAssociateVPCMissingZone(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	_, err := client.AssociateVPCWithHostedZone(ctx, &awsr53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String("ZDOESNOTEXIST"),
		VPC:          &r53types.VPC{VPCId: aws.String("vpc-1111"), VPCRegion: r53types.VPCRegionUsEast1},
	})
	if err == nil {
		t.Fatal("AssociateVPCWithHostedZone(missing zone) succeeded, want NoSuchHostedZone")
	}

	var notFound *r53types.NoSuchHostedZone
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want *NoSuchHostedZone", err)
	}
}

// TestSDKListHostedZonesByVPC proves the private zones a VPC is associated with
// are listed, and unassociated zones are excluded.
func TestSDKListHostedZonesByVPC(t *testing.T) {
	client := newRoute53Client(t)
	ctx := context.Background()

	shared := createPrivateZone(t, client, "shared.internal.", "shared-ref", "vpc-shared")
	createPrivateZone(t, client, "other.internal.", "other-ref", "vpc-other")

	out, err := client.ListHostedZonesByVPC(ctx, &awsr53.ListHostedZonesByVPCInput{
		VPCId:     aws.String("vpc-shared"),
		VPCRegion: r53types.VPCRegionUsEast1,
	})
	if err != nil {
		t.Fatalf("ListHostedZonesByVPC: %v", err)
	}

	if len(out.HostedZoneSummaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(out.HostedZoneSummaries))
	}

	if aws.ToString(out.HostedZoneSummaries[0].HostedZoneId) != shared {
		t.Errorf("summary zone id = %q, want %q",
			aws.ToString(out.HostedZoneSummaries[0].HostedZoneId), shared)
	}

	if out.HostedZoneSummaries[0].Owner == nil ||
		aws.ToString(out.HostedZoneSummaries[0].Owner.OwningAccount) == "" {
		t.Errorf("summary Owner = %+v, want an OwningAccount", out.HostedZoneSummaries[0].Owner)
	}
}
