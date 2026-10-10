package persist_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/persist"
	awsprov "github.com/stackshy/cloudemu/v2/providers/aws"
	azureprov "github.com/stackshy/cloudemu/v2/providers/azure"
	gcpprov "github.com/stackshy/cloudemu/v2/providers/gcp"
	ociprov "github.com/stackshy/cloudemu/v2/providers/oci"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	sdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// idCase creates one resource of a family on a provider and returns its id.
// seq is 1 for the resource created before the snapshot and 2 for the one
// created after the restore, so names stay distinct.
type idCase[P any] struct {
	name   string
	create func(ctx context.Context, p P, seq int) (string, error)
}

// checkNoIDReuse creates a resource, snapshots the provider, resets the shared
// id counter (a fresh process), restores into a fresh provider and creates
// another one. The second id must differ from the first, and both resources
// must be in the restored provider's state. legacy drops the recorded counter,
// as in a snapshot written before it was recorded.
func checkNoIDReuse[P any](t *testing.T, key string, newP func() P, svcs func(P) persist.Services, tc idCase[P], legacy bool) {
	t.Helper()

	ctx := t.Context()

	// Both providers start from a zero counter, like two runs of a process.
	idgen.Reset()

	src := newP()

	first, err := tc.create(ctx, src, 1)
	if err != nil {
		t.Fatalf("create before snapshot: %v", err)
	}

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{key: svcs(src)}, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	if legacy {
		ps := snap.Providers[key]
		ps.IDCounter = 0
		snap.Providers[key] = ps
	} else if got, want := snap.Providers[key].IDCounter, idgen.Counter(); got != want {
		t.Fatalf("snapshot recorded id counter %d, want %d", got, want)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded persist.Snapshot
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	idgen.Reset()

	dst := newP()
	if err := persist.RestoreAll(ctx, &loaded, map[string]persist.Services{key: svcs(dst)}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	second, err := tc.create(ctx, dst, 2)
	if err != nil {
		t.Fatalf("create after restore: %v", err)
	}

	if second == first {
		t.Fatalf("id %q reused after restore", first)
	}

	after, err := persist.ExportAll(ctx, map[string]persist.Services{key: svcs(dst)}, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll after restore: %v", err)
	}

	state, _ := json.Marshal(after)
	for _, id := range []string{first, second} {
		if !strings.Contains(string(state), id) {
			t.Fatalf("restored state lost %q", id)
		}
	}
}

func runIDCases[P any](t *testing.T, key string, newP func() P, svcs func(P) persist.Services, cases []idCase[P]) {
	t.Helper()

	for _, tc := range cases {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%v", tc.name, legacy), func(t *testing.T) {
				checkNoIDReuse(t, key, newP, svcs, tc, legacy)
			})
		}
	}
}

func TestRestoreAdvancesIDCounterAWS(t *testing.T) {
	cases := []idCase[*awsprov.Provider]{
		{"ec2 instance", func(ctx context.Context, p *awsprov.Provider, _ int) (string, error) {
			out, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-123", InstanceType: "t3.micro"}, 1)
			if err != nil {
				return "", err
			}

			return out[0].ID, nil
		}},
		{"vpc", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			v, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: fmt.Sprintf("10.%d.0.0/16", seq)})
			if err != nil {
				return "", err
			}

			return v.ID, nil
		}},
		{"security group", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			v, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: fmt.Sprintf("10.%d.0.0/16", 10+seq)})
			if err != nil {
				return "", err
			}

			sg, err := p.VPC.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
				Name: fmt.Sprintf("app-%d", seq), Description: "app", VPCID: v.ID,
			})
			if err != nil {
				return "", err
			}

			return sg.ID, nil
		}},
		{"s3 version id", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			if seq == 1 {
				if err := p.S3.CreateBucket(ctx, "versioned"); err != nil {
					return "", err
				}

				if err := p.S3.SetBucketVersioning(ctx, "versioned", true); err != nil {
					return "", err
				}
			}

			if err := p.S3.PutObject(ctx, "versioned", "k", []byte(fmt.Sprint(seq)), "text/plain", nil); err != nil {
				return "", err
			}

			return latestVersion(ctx, p, "versioned", "k")
		}},
		{"iam policy", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			pol, err := p.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
				Name:           fmt.Sprintf("pol-%d", seq),
				PolicyDocument: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
			})
			if err != nil {
				return "", err
			}

			return pol.ID, nil
		}},
		{"kms key", func(ctx context.Context, p *awsprov.Provider, _ int) (string, error) {
			k, err := p.KMS.CreateKey(ctx, kmsdriver.CreateKeyInput{})
			if err != nil {
				return "", err
			}

			return k.KeyID, nil
		}},
		{"secrets manager secret", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			s, err := p.SecretsManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: fmt.Sprintf("s%d", seq)}, []byte("v"))
			if err != nil {
				return "", err
			}

			return s.ID, nil
		}},
		{"sns subscription", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			topic, err := p.SNS.CreateTopic(ctx, notifdriver.TopicConfig{Name: fmt.Sprintf("t%d", seq)})
			if err != nil {
				return "", err
			}

			sub, err := p.SNS.Subscribe(ctx, notifdriver.SubscriptionConfig{
				TopicID: topic.Name, Protocol: "sqs", Endpoint: "arn:aws:sqs:us-east-1:123456789012:q",
			})
			if err != nil {
				return "", err
			}

			return sub.ID, nil
		}},
		{"route53 zone", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			z, err := p.Route53.CreateZone(ctx, dnsdriver.ZoneConfig{Name: fmt.Sprintf("z%d.example.com", seq)})
			if err != nil {
				return "", err
			}

			return z.ID, nil
		}},
		{"elbv2 target group", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			tg, err := p.ELB.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
				Name: fmt.Sprintf("tg-%d", seq), Protocol: "HTTP", Port: 80,
			})
			if err != nil {
				return "", err
			}

			return tg.ARN, nil
		}},
		{"lambda event source mapping", func(ctx context.Context, p *awsprov.Provider, seq int) (string, error) {
			fn := fmt.Sprintf("fn%d", seq)
			if _, err := p.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: fn}); err != nil {
				return "", err
			}

			m, err := p.Lambda.CreateEventSourceMapping(ctx, sdriver.EventSourceMappingConfig{
				FunctionName: fn, EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:q",
			})
			if err != nil {
				return "", err
			}

			return m.UUID, nil
		}},
	}

	runIDCases(t, "aws", func() *awsprov.Provider { return cloudemu.NewAWS() }, func(p *awsprov.Provider) persist.Services { return p.SnapshotServices() }, cases)
}

func latestVersion(ctx context.Context, p *awsprov.Provider, bucket, key string) (string, error) {
	vl, err := p.S3.ListObjectVersions(ctx, bucket, storagedriver.ListOptions{})
	if err != nil {
		return "", err
	}

	for _, v := range vl.Versions {
		if v.Key == key && v.IsLatest {
			return v.VersionID, nil
		}
	}

	return "", fmt.Errorf("no latest version of %s", key)
}

func TestRestoreAdvancesIDCounterAzure(t *testing.T) {
	cases := []idCase[*azureprov.Provider]{
		{"vnet", func(ctx context.Context, p *azureprov.Provider, seq int) (string, error) {
			v, err := p.VNet.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: fmt.Sprintf("10.%d.0.0/16", seq)})
			if err != nil {
				return "", err
			}

			return v.ID, nil
		}},
		{"vm", func(ctx context.Context, p *azureprov.Provider, _ int) (string, error) {
			out, err := p.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
				ImageID: "ubuntu-22", InstanceType: "Standard_D2s_v3",
			}, 1)
			if err != nil {
				return "", err
			}

			return out[0].ID, nil
		}},
		{"key vault secret", func(ctx context.Context, p *azureprov.Provider, seq int) (string, error) {
			s, err := p.KeyVault.CreateSecret(ctx, secretsdriver.SecretConfig{Name: fmt.Sprintf("s%d", seq)}, []byte("v"))
			if err != nil {
				return "", err
			}

			return s.ID, nil
		}},
		{"functions event source mapping", func(ctx context.Context, p *azureprov.Provider, seq int) (string, error) {
			fn := fmt.Sprintf("fn%d", seq)
			if _, err := p.Functions.CreateFunction(ctx, sdriver.FunctionConfig{Name: fn}); err != nil {
				return "", err
			}

			m, err := p.Functions.CreateEventSourceMapping(ctx, sdriver.EventSourceMappingConfig{
				FunctionName: fn, EventSourceArn: "queue",
			})
			if err != nil {
				return "", err
			}

			return m.UUID, nil
		}},
	}

	runIDCases(t, "azure", func() *azureprov.Provider { return cloudemu.NewAzure() }, func(p *azureprov.Provider) persist.Services { return p.SnapshotServices() }, cases)
}

func TestRestoreAdvancesIDCounterGCP(t *testing.T) {
	cases := []idCase[*gcpprov.Provider]{
		{"vpc network", func(ctx context.Context, p *gcpprov.Provider, seq int) (string, error) {
			v, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: fmt.Sprintf("10.%d.0.0/16", seq)})
			if err != nil {
				return "", err
			}

			return v.ID, nil
		}},
		{"gce instance", func(ctx context.Context, p *gcpprov.Provider, _ int) (string, error) {
			out, err := p.GCE.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "debian-12", InstanceType: "e2-micro"}, 1)
			if err != nil {
				return "", err
			}

			return out[0].ID, nil
		}},
		{"secret manager secret", func(ctx context.Context, p *gcpprov.Provider, seq int) (string, error) {
			s, err := p.SecretManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: fmt.Sprintf("s%d", seq)}, []byte("v"))
			if err != nil {
				return "", err
			}

			return s.ID, nil
		}},
		{"cloud functions event source mapping", func(ctx context.Context, p *gcpprov.Provider, seq int) (string, error) {
			fn := fmt.Sprintf("fn%d", seq)
			if _, err := p.CloudFunctions.CreateFunction(ctx, sdriver.FunctionConfig{Name: fn}); err != nil {
				return "", err
			}

			m, err := p.CloudFunctions.CreateEventSourceMapping(ctx, sdriver.EventSourceMappingConfig{
				FunctionName: fn, EventSourceArn: "topic",
			})
			if err != nil {
				return "", err
			}

			return m.UUID, nil
		}},
	}

	runIDCases(t, "gcp", func() *gcpprov.Provider { return cloudemu.NewGCP() }, func(p *gcpprov.Provider) persist.Services { return p.SnapshotServices() }, cases)
}

func TestRestoreAdvancesIDCounterOCI(t *testing.T) {
	cases := []idCase[*ociprov.Provider]{
		{"vcn", func(ctx context.Context, p *ociprov.Provider, seq int) (string, error) {
			v, err := p.VCN.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: fmt.Sprintf("10.%d.0.0/16", seq)})
			if err != nil {
				return "", err
			}

			return v.ID, nil
		}},
		{"identity user", func(ctx context.Context, p *ociprov.Provider, seq int) (string, error) {
			u, err := p.Identity.CreateUser(ctx, iamdriver.UserConfig{Name: fmt.Sprintf("u%d", seq)})
			if err != nil {
				return "", err
			}

			return u.ID, nil
		}},
		{"ons topic", func(ctx context.Context, p *ociprov.Provider, seq int) (string, error) {
			ch, err := p.Monitoring.CreateNotificationChannel(ctx, mondriver.NotificationChannelConfig{
				Name: fmt.Sprintf("topic%d", seq), Type: "email", Endpoint: "ops@example.com",
			})
			if err != nil {
				return "", err
			}

			return ch.ID, nil
		}},
	}

	runIDCases(t, "oci", func() *ociprov.Provider { return cloudemu.NewOCI() }, func(p *ociprov.Provider) persist.Services { return p.SnapshotServices() }, cases)
}

// TestRestoreDoesNotRewindIDCounter loads an old snapshot into a process that
// has already minted more ids: the counter must keep its higher value.
func TestRestoreDoesNotRewindIDCounter(t *testing.T) {
	ctx := t.Context()
	src := cloudemu.NewAWS()

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"aws": src.SnapshotServices()}, persist.Options{})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	idgen.AdvanceTo(snap.Providers["aws"].IDCounter + 1000)
	before := idgen.Counter()

	dst := cloudemu.NewAWS()
	if err := persist.RestoreAll(ctx, &snap, map[string]persist.Services{"aws": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	if got := idgen.Counter(); got < before {
		t.Fatalf("counter rewound from %d to %d", before, got)
	}
}

// TestSingleProviderRestoreAdvancesIDCounter covers the per-provider
// Export/Restore entry points, not just ExportAll/RestoreAll.
func TestSingleProviderRestoreAdvancesIDCounter(t *testing.T) {
	ctx := t.Context()
	src := cloudemu.NewAWS()

	if _, err := src.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-1", InstanceType: "t3.micro"}, 1); err != nil {
		t.Fatalf("run instances: %v", err)
	}

	ps, err := persist.Export(ctx, src.SnapshotServices(), persist.Options{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	idgen.Reset()

	if err := persist.Restore(ctx, cloudemu.NewAWS().SnapshotServices(), &ps); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := idgen.Counter(); got < ps.IDCounter {
		t.Fatalf("counter %d not advanced to %d", got, ps.IDCounter)
	}
}
