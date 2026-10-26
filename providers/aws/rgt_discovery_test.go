package aws

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
	guarddutydriver "github.com/stackshy/cloudemu/v2/services/guardduty/driver"
	kafkadriver "github.com/stackshy/cloudemu/v2/services/kafka/driver"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
	opensearchdriver "github.com/stackshy/cloudemu/v2/services/opensearch/driver"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// A resource deleted between the list call and its tag read makes the tag read
// return NotFound. project must drop that resource instead of failing, since
// the error would otherwise abort DiscoverResources for every service. Any
// other error must still propagate.
func TestProjectSkipsVanishedResource(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	read := func(_ context.Context, name string) (resourcediscovery.DiscoveredResource, error) {
		switch name {
		case "gone":
			return resourcediscovery.DiscoveredResource{}, cerrors.Newf(cerrors.NotFound, "resource %q not found", name)
		case "broken":
			return resourcediscovery.DiscoveredResource{}, boom
		default:
			return row("svc", "thing", name, "arn:"+name, nil), nil
		}
	}

	rows, err := project(ctx, []string{"a", "gone", "b"}, read)
	if err != nil {
		t.Fatalf("project with a vanished resource: %v", err)
	}

	if len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" {
		t.Fatalf("rows = %+v, want a and b only", rows)
	}

	if _, err := project(ctx, []string{"a", "broken"}, read); !errors.Is(err, boom) {
		t.Fatalf("project with a failing read: err = %v, want boom", err)
	}
}

// A DeleteKeyspace racing the Keyspaces discovery walk must not fail it.
// ListTagsForResource returns NotFound for a keyspace that is gone, so without
// the skip in project nearly every walk that overlaps the delete loop fails.
func TestKeyspacesDiscoverySurvivesConcurrentDelete(t *testing.T) {
	ctx := context.Background()
	p := New()
	d := taggedDiscovery{p}

	if _, err := p.Keyspaces.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{
		Name: "stable", Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateKeyspace stable: %v", err)
	}

	stop := make(chan struct{})

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		for {
			select {
			case <-stop:
				return
			default:
			}

			_, _ = p.Keyspaces.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "churn"})
			_ = p.Keyspaces.DeleteKeyspace(ctx, "churn")
		}
	}()

	defer func() {
		close(stop)
		wg.Wait()
	}()

	for i := 0; i < 2000; i++ {
		rows, err := d.discoverKeyspaces(ctx)
		if err != nil {
			t.Fatalf("walk %d: discoverKeyspaces: %v", i, err)
		}

		var found bool

		for _, r := range rows {
			if r.ID == "stable" && r.Tags["env"] == "prod" {
				found = true
			}
		}

		if !found {
			t.Fatalf("walk %d: stable keyspace missing or untagged: %+v", i, rows)
		}
	}
}

// discoveryChurnCase drives one discovery walker while another goroutine keeps
// creating and deleting a resource of the same service.
type discoveryChurnCase struct {
	name string
	// setup creates the long-lived resource the walk must keep reporting. It
	// returns that resource's ID, or "" for a service that allows only one
	// resource (GuardDuty has one detector per account and Region).
	setup func(ctx context.Context, t *testing.T, p *Provider) string
	// churn creates and deletes one short-lived resource.
	churn func(ctx context.Context, p *Provider)
	walk  func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error)
}

func discoveryChurnCases() []discoveryChurnCase {
	tagged := map[string]string{"env": "prod"}

	return []discoveryChurnCase{
		{
			name: "kafka",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				if _, err := p.Kafka.CreateCluster(ctx, kafkaCluster("stable", tagged)); err != nil {
					t.Fatalf("CreateCluster: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if c, err := p.Kafka.CreateCluster(ctx, kafkaCluster("churn", nil)); err == nil {
					_, _, _ = p.Kafka.DeleteCluster(ctx, c.ClusterARN, "")
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return taggedDiscovery{p}.discoverKafka(ctx)
			},
		},
		{
			name: "kinesis",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				err := p.Kinesis.CreateStream(ctx, kinesisdriver.CreateStreamInput{StreamName: "stable", ShardCount: 1, Tags: tagged})
				if err != nil {
					t.Fatalf("CreateStream: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if p.Kinesis.CreateStream(ctx, kinesisdriver.CreateStreamInput{StreamName: "churn", ShardCount: 1}) == nil {
					_ = p.Kinesis.DeleteStream(ctx, "churn", "", true)
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return taggedDiscovery{p}.discoverKinesis(ctx)
			},
		},
		{
			name: "opensearch",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				if _, err := p.OpenSearch.CreateDomain(ctx, openSearchDomain("stable", tagged)); err != nil {
					t.Fatalf("CreateDomain: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if _, err := p.OpenSearch.CreateDomain(ctx, openSearchDomain("churn", nil)); err == nil {
					_, _ = p.OpenSearch.DeleteDomain(ctx, "churn")
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return taggedDiscovery{p}.discoverOpenSearch(ctx)
			},
		},
		{
			name: "guardduty",
			setup: func(context.Context, *testing.T, *Provider) string {
				return ""
			},
			churn: func(ctx context.Context, p *Provider) {
				if det, err := p.GuardDuty.CreateDetector(ctx, guarddutydriver.CreateDetectorInput{Enable: true, Tags: tagged}); err == nil {
					_ = p.GuardDuty.DeleteDetector(ctx, det.ID)
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return taggedDiscovery{p}.discoverGuardDuty(ctx)
			},
		},
		{
			name: "glue",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				if _, err := p.Glue.CreateJob(ctx, gluedriver.Job{Name: "stable", Role: "r", Tags: tagged}); err != nil {
					t.Fatalf("CreateJob: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if _, err := p.Glue.CreateJob(ctx, gluedriver.Job{Name: "churn", Role: "r"}); err == nil {
					_, _ = p.Glue.DeleteJob(ctx, "churn")
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return taggedDiscovery{p}.discoverGlue(ctx)
			},
		},
		{
			name: "sagemaker",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				if _, err := p.SageMaker.CreateModel(ctx, sagemakerdriver.ModelConfig{
					ModelName: "stable", Tags: []sagemakerdriver.Tag{{Key: "env", Value: "prod"}},
				}); err != nil {
					t.Fatalf("CreateModel: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if _, err := p.SageMaker.CreateModel(ctx, sagemakerdriver.ModelConfig{ModelName: "churn"}); err == nil {
					_ = p.SageMaker.DeleteModel(ctx, "churn")
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				return sagemakerDiscovery{p.SageMaker}.DiscoverResources(ctx)
			},
		},
		{
			name: "redshift",
			setup: func(ctx context.Context, t *testing.T, p *Provider) string {
				t.Helper()

				if _, err := p.Redshift.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "stable", Tags: tagged}); err != nil {
					t.Fatalf("CreateCluster: %v", err)
				}

				return "stable"
			},
			churn: func(ctx context.Context, p *Provider) {
				if _, err := p.Redshift.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: "churn"}); err == nil {
					_ = p.Redshift.DeleteCluster(ctx, "churn")
				}
			},
			walk: func(ctx context.Context, p *Provider) ([]resourcediscovery.DiscoveredResource, error) {
				dbs, err := rdsDiscovery{m: p.RDS, redshift: p.Redshift}.discoverRedshift(ctx)

				out := make([]resourcediscovery.DiscoveredResource, 0, len(dbs))
				for _, db := range dbs {
					out = append(out, resourcediscovery.DiscoveredResource{ID: db.Name, ARN: db.ARN, Tags: db.Tags})
				}

				return out, err
			},
		},
	}
}

// A delete racing a discovery walk must not fail it, for every walker that
// reads tags in a call separate from its list call. The long-lived resource
// must keep surfacing with its tags on every walk.
func TestDiscoverySurvivesConcurrentDelete(t *testing.T) {
	for _, tc := range discoveryChurnCases() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p := New()
			stable := tc.setup(ctx, t, p)

			stop := make(chan struct{})

			var wg sync.WaitGroup

			wg.Add(1)

			go func() {
				defer wg.Done()

				for {
					select {
					case <-stop:
						return
					default:
					}

					tc.churn(ctx, p)
				}
			}()

			defer func() {
				close(stop)
				wg.Wait()
			}()

			for i := 0; i < 2000; i++ {
				rows, err := tc.walk(ctx, p)
				if err != nil {
					t.Fatalf("walk %d: %v", i, err)
				}

				if stable != "" && !hasTaggedRow(rows, stable) {
					t.Fatalf("walk %d: %s missing or untagged: %+v", i, stable, rows)
				}
			}
		})
	}
}

func hasTaggedRow(rows []resourcediscovery.DiscoveredResource, id string) bool {
	for _, r := range rows {
		if r.ID == id && r.Tags["env"] == "prod" {
			return true
		}
	}

	return false
}

func kafkaCluster(name string, tags map[string]string) kafkadriver.CreateClusterInput {
	return kafkadriver.CreateClusterInput{
		ClusterName: name, KafkaVersion: "3.6.0", NumberOfBrokerNodes: 2,
		BrokerNodeGroupInfo: &kafkadriver.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2"}, InstanceType: "kafka.m5.large",
		},
		Tags: tags,
	}
}

func openSearchDomain(name string, tags map[string]string) opensearchdriver.CreateDomainInput {
	return opensearchdriver.CreateDomainInput{
		DomainName: name, EngineVersion: "OpenSearch_2.11",
		ClusterConfig: opensearchdriver.ClusterConfig{InstanceType: "t3.small.search", InstanceCount: 1},
		Tags:          tags,
	}
}

// Each paginated or non-project walker reads tags with its own row function.
// These tests take the list the walker would see, delete one resource, and
// then project that stale list through the walker's row function. The deleted
// resource must be skipped and the live one must still surface with its tags.
func TestTaggedWalkersSkipResourceDeletedAfterList(t *testing.T) {
	ctx := context.Background()
	tagged := map[string]string{"env": "prod"}

	t.Run("kafka", func(t *testing.T) {
		p := New()
		d := taggedDiscovery{p}

		if _, err := p.Kafka.CreateCluster(ctx, kafkaCluster("live", tagged)); err != nil {
			t.Fatal(err)
		}

		gone, err := p.Kafka.CreateCluster(ctx, kafkaCluster("gone", nil))
		mustNoErr(t, err)

		listed, _, err := p.Kafka.ListClusters(ctx, "", kafkadriver.Page{MaxResults: pageLimit})
		mustNoErr(t, err)

		_, _, err = p.Kafka.DeleteCluster(ctx, gone.ClusterARN, "")
		mustNoErr(t, err)

		rows, err := project(ctx, listed, d.kafkaRow)
		wantOnlyTagged(t, rows, err, "live")
	})

	t.Run("kinesis", func(t *testing.T) {
		p := New()
		d := taggedDiscovery{p}

		mustNoErr(t, p.Kinesis.CreateStream(ctx, kinesisdriver.CreateStreamInput{StreamName: "live", ShardCount: 1, Tags: tagged}))
		mustNoErr(t, p.Kinesis.CreateStream(ctx, kinesisdriver.CreateStreamInput{StreamName: "gone", ShardCount: 1}))

		listed, err := p.Kinesis.ListStreams(ctx, "", "", pageLimit)
		mustNoErr(t, err)
		mustNoErr(t, p.Kinesis.DeleteStream(ctx, "gone", "", true))

		rows, err := project(ctx, listed.StreamSummaries, d.kinesisRow)
		wantOnlyTagged(t, rows, err, "live")
	})

	t.Run("opensearch", func(t *testing.T) {
		p := New()
		d := taggedDiscovery{p}

		if _, err := p.OpenSearch.CreateDomain(ctx, openSearchDomain("live", tagged)); err != nil {
			t.Fatal(err)
		}

		if _, err := p.OpenSearch.CreateDomain(ctx, openSearchDomain("gone", nil)); err != nil {
			t.Fatal(err)
		}

		listed, err := p.OpenSearch.ListDomainNames(ctx, "")
		mustNoErr(t, err)

		if _, err := p.OpenSearch.DeleteDomain(ctx, "gone"); err != nil {
			t.Fatal(err)
		}

		rows, err := project(ctx, listed, d.openSearchRow)
		wantOnlyTagged(t, rows, err, "live")
	})

	t.Run("glue", func(t *testing.T) {
		p := New()
		d := taggedDiscovery{p}

		if _, err := p.Glue.CreateJob(ctx, gluedriver.Job{Name: "live", Role: "r", Tags: tagged}); err != nil {
			t.Fatal(err)
		}

		if _, err := p.Glue.CreateJob(ctx, gluedriver.Job{Name: "gone", Role: "r"}); err != nil {
			t.Fatal(err)
		}

		listed, _, err := p.Glue.ListJobs(ctx, gluedriver.TablePagination{MaxResults: pageLimit})
		mustNoErr(t, err)

		if _, err := p.Glue.DeleteJob(ctx, "gone"); err != nil {
			t.Fatal(err)
		}

		rows, err := project(ctx, listed, d.glueJobRow)
		wantOnlyTagged(t, rows, err, "live")
	})

	// GuardDuty allows one detector per account and Region, so there is no
	// live detector to keep. The deleted one must be skipped even though
	// GetDetector reports it as BadRequest, not NotFound.
	t.Run("guardduty", func(t *testing.T) {
		p := New()
		d := taggedDiscovery{p}

		det, err := p.GuardDuty.CreateDetector(ctx, guarddutydriver.CreateDetectorInput{Enable: true, Tags: tagged})
		mustNoErr(t, err)

		listed, err := d.listGuardDutyDetectors(ctx)
		mustNoErr(t, err)

		rows, err := project(ctx, listed, d.guardDutyRow)
		wantOnlyTagged(t, rows, err, det.ID)

		mustNoErr(t, p.GuardDuty.DeleteDetector(ctx, det.ID))

		if _, err := p.GuardDuty.GetDetector(ctx, det.ID); err == nil || cerrors.IsNotFound(err) {
			t.Fatalf("GetDetector on a deleted detector: err = %v, want a non-NotFound error", err)
		}

		rows, err = project(ctx, listed, d.guardDutyRow)
		if err != nil || len(rows) != 0 {
			t.Fatalf("rows = %+v, err = %v, want no rows and no error", rows, err)
		}
	})
}

// The SageMaker walker must skip a resource whose tag read reports NotFound and
// still fail on any other tag-read error.
func TestSageMakerDiscoverySkipsVanishedResource(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	for _, tc := range []struct {
		name    string
		readErr error
		wantErr error
	}{
		{name: "not found", readErr: cerrors.Newf(cerrors.NotFound, "model gone")},
		{name: "other error", readErr: boom, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New()

			for _, name := range []string{"live", "gone"} {
				_, err := p.SageMaker.CreateModel(ctx, sagemakerdriver.ModelConfig{
					ModelName: name, Tags: []sagemakerdriver.Tag{{Key: "env", Value: "prod"}},
				})
				mustNoErr(t, err)
			}

			m := failingTagRead{smMock: p.SageMaker, failARN: "model/gone", err: tc.readErr}
			rows, err := sagemakerDiscovery{m}.DiscoverResources(ctx)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}

				return
			}

			wantOnlyTagged(t, rows, err, "live")
		})
	}
}

// failingTagRead wraps the SageMaker mock and fails ListTags for one ARN, as if
// that resource were deleted after the walk listed it.
type failingTagRead struct {
	smMock

	failARN string
	err     error
}

func (f failingTagRead) ListTags(ctx context.Context, arn string) ([]sagemakerdriver.Tag, error) {
	if strings.HasSuffix(arn, f.failARN) {
		return nil, f.err
	}

	return f.smMock.ListTags(ctx, arn)
}

// The Redshift walker must skip a cluster whose tag read reports NotFound and
// still fail on any other tag-read error.
func TestRedshiftDiscoverySkipsVanishedCluster(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	for _, tc := range []struct {
		name    string
		readErr error
		wantErr error
	}{
		{name: "not found", readErr: cerrors.Newf(cerrors.NotFound, "cluster gone")},
		{name: "other error", readErr: boom, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := New()

			for _, id := range []string{"live", "gone"} {
				_, err := p.Redshift.CreateCluster(ctx, rdbdriver.ClusterConfig{ID: id, Tags: map[string]string{"env": "prod"}})
				mustNoErr(t, err)
			}

			rs := failingRedshiftTags{redshiftClusters: p.Redshift, failARN: "cluster:gone", err: tc.readErr}
			dbs, err := rdsDiscovery{m: p.RDS, redshift: rs}.discoverRedshift(ctx)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil || len(dbs) != 1 || dbs[0].Name != "live" || dbs[0].Tags["env"] != "prod" {
				t.Fatalf("dbs = %+v, err = %v, want only the tagged live cluster", dbs, err)
			}
		})
	}
}

// failingRedshiftTags wraps the Redshift mock and fails DescribeTags for one
// ARN, as if that cluster were deleted after DescribeClusters.
type failingRedshiftTags struct {
	redshiftClusters

	failARN string
	err     error
}

func (f failingRedshiftTags) DescribeTags(ctx context.Context, arn string) (map[string]string, error) {
	if strings.HasSuffix(arn, f.failARN) {
		return nil, f.err
	}

	return f.redshiftClusters.DescribeTags(ctx, arn)
}

func wantOnlyTagged(t *testing.T, rows []resourcediscovery.DiscoveredResource, err error, id string) {
	t.Helper()

	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(rows) != 1 || !hasTaggedRow(rows, id) {
		t.Fatalf("rows = %+v, want only %s tagged env=prod", rows, id)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
