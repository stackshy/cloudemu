package gcp_test

// Golden routing table for the assembled GCP server. Each row records which
// handler type srv.Match picks for a request, so a change to any Matches rule
// shows up as a diff of this table. Seeding goes through ServeHTTP so the
// ownership predicates see real state.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
)

const (
	goldenLoc  = "/v1/projects/demo/locations/us-central1"
	goldenWest = "/v1/projects/demo/locations/europe-west1"

	goldenKafkaBody = `{"capacityConfig":{"vcpuCount":"3","memoryBytes":"3221225472"},` +
		`"gcpConfig":{"accessConfig":{"networkConfigs":[{"subnet":"projects/demo/regions/us-central1/subnetworks/s"}]}}}`
	goldenFilestoreBody = `{"tier":"BASIC_HDD","fileShares":[{"name":"s","capacityGb":"1024"}],"networks":[{"network":"default"}]}`
	goldenRedisBody     = `{"tier":"BASIC","memorySizeGb":1}`
)

type goldenRow struct {
	scenario string // "empty" or "seeded"
	method   string
	path     string
	host     string
	body     string
	want     string
}

// goldenSeed creates one resource per colliding service so ownership rules
// have something to see.
func goldenSeed(t *testing.T, srv *server.Server) {
	t.Helper()

	westKafka := strings.ReplaceAll(goldenKafkaBody, "us-central1", "europe-west1")

	seeds := []struct{ method, path, body string }{
		{http.MethodPost, goldenLoc + "/clusters", `{"cluster":{"name":"c1","initialNodeCount":1}}`},
		{http.MethodPost, goldenLoc + "/clusters?clusterId=k3", goldenKafkaBody},
		{http.MethodPost, goldenWest + "/clusters?clusterId=k2", westKafka},
		{http.MethodPost, goldenLoc + "/instances?instanceId=f1", goldenFilestoreBody},
		{http.MethodPost, goldenLoc + "/instances?instanceId=r1", goldenRedisBody},
		{http.MethodPost, goldenLoc + "/instances?instanceId=d1", `{"type":"BASIC"}`},
		{http.MethodPost, goldenLoc + "/instances?instanceId=s1", `{}`},
	}

	for _, s := range seeds {
		req := httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("seed %s %s: code=%d body=%s", s.method, s.path, rec.Code, rec.Body.String())
		}
	}
}

func goldenRows() []goldenRow {
	const (
		e = "empty"
		s = "seeded"
		g = http.MethodGet
		p = http.MethodPost
		d = http.MethodDelete
		u = http.MethodPatch
	)

	return []goldenRow{
		// clusters group: GKE, Managed Kafka (AlloyDB is not mounted beside GKE).
		{e, g, goldenLoc + "/clusters", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters", "", "", "*gke.Handler"},
		{s, g, goldenWest + "/clusters", "", "", "*managedkafka.Handler"},
		{e, g, "/v1/projects/demo/locations/-/clusters", "", "", "*gke.Handler"},
		{s, g, "/v1/projects/demo/locations/-/clusters", "", "", "*gke.Handler"},
		{e, p, goldenLoc + "/clusters", "", `{"cluster":{"name":"g9"}}`, "*gke.Handler"},
		{e, p, goldenLoc + "/clusters?clusterId=k9", "", goldenKafkaBody, "*managedkafka.Handler"},
		{e, p, goldenLoc + "/clusters?clusterId=a9", "", `{"networkConfig":{"network":"projects/demo/global/networks/default"}}`, "*gke.Handler"},
		{s, p, goldenLoc + "/clusters?clusterId=k3", "", `{}`, "*managedkafka.Handler"},
		{e, p, goldenLoc + "/clusters:restore", "", `{}`, "*gke.Handler"},
		{e, p, goldenLoc + "/clusters:createsecondary", "", `{}`, "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/k3", "", "", "*managedkafka.Handler"},
		{s, g, goldenLoc + "/clusters/nope", "", "", "*gke.Handler"},
		{s, g, goldenWest + "/clusters/k2", "", "", "*managedkafka.Handler"},
		{s, u, goldenLoc + "/clusters/k3", "", `{"labels":{"a":"b"}}`, "*managedkafka.Handler"},
		{s, u, goldenLoc + "/clusters/c1", "", `{"labels":{"a":"b"}}`, "*gke.Handler"},
		{s, u, goldenLoc + "/clusters/nope?updateMask=labels", "", `{"labels":{"a":"b"}}`, "*managedkafka.Handler"},
		{s, u, goldenLoc + "/clusters/nope:upgrade", "", `{}`, "*gke.Handler"},
		{s, http.MethodPut, goldenLoc + "/clusters/c1", "", `{"update":{}}`, "*gke.Handler"},
		{s, d, goldenLoc + "/clusters/c1", "", "", "*gke.Handler"},
		{s, d, goldenLoc + "/clusters/k3", "", "", "*managedkafka.Handler"},
		{s, d, goldenLoc + "/clusters/nope", "", "", "*gke.Handler"},
		{s, p, goldenLoc + "/clusters/c1:setLogging", "", `{}`, "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1:checkAutopilotCompatibility", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1:fetchClusterUpgradeInfo", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/nodePools", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/nodePools/np1", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/nodePools/np1:fetchNodePoolUpgradeInfo", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/jwks", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/.well-known/openid-configuration", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/c1/bogus", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/k3/topics", "", "", "*managedkafka.Handler"},
		{s, g, goldenLoc + "/clusters/k3/topics/t1", "", "", "*managedkafka.Handler"},
		{s, g, goldenLoc + "/clusters/k3/consumerGroups", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/k3/acls", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/a1/instances", "", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters/a1/users", "", "", "*gke.Handler"},
		{e, g, goldenLoc + "/backups", "", "", "*firestore.Handler"},
		{e, g, goldenLoc + "/supportedDatabaseFlags", "", "", "*firestore.Handler"},
		{e, g, goldenLoc + "/serverConfig", "", "", "*gke.Handler"},
		{e, g, goldenLoc + "/operations", "", "", "*gke.Handler"},
		{e, g, goldenLoc + "/operations/nope", "", "", "*lro.Handler"},
		{e, p, goldenLoc + "/operations/nope:cancel", "", `{}`, "*lro.Handler"},
		{e, g, "/v1/projects/demo/zones/us-central1-a/clusters", "", "", "*firestore.Handler"},
		{e, g, "/v1/projects/demo/aggregated/usableSubnetworks", "", "", "*firestore.Handler"},
		{e, g, "/v1beta1/projects/demo/locations/us-central1/clusters", "", "", "<nil>"},
		{e, g, "/v1beta/projects/demo/locations/us-central1/clusters", "", "", "<nil>"},

		// instances group: Data Fusion, Secure Source Manager, Filestore, Memorystore.
		{e, g, goldenLoc + "/instances", "", "", "*memorystore.Handler"},
		{s, g, goldenLoc + "/instances", "", "", "*datafusion.Handler"},
		{e, g, "/v1/projects/demo/locations/us-central1-a/instances", "", "", "*memorystore.Handler"},
		{s, g, "/v1/projects/demo/locations/us-central1-a/instances", "", "", "*memorystore.Handler"},
		{e, p, goldenLoc + "/instances?instanceId=x", "", goldenFilestoreBody, "*filestore.Handler"},
		{e, p, goldenLoc + "/instances?instanceId=x", "", goldenRedisBody, "*memorystore.Handler"},
		{e, p, goldenLoc + "/instances?instanceId=x", "", `{"type":"BASIC"}`, "*datafusion.Handler"},
		{e, p, goldenLoc + "/instances?instanceId=x", "", `{}`, "*securesourcemanager.Handler"},
		{e, p, goldenLoc + "/instances?instanceId=x", "", `{"tier":"NOPE"}`, "*memorystore.Handler"},
		{s, g, goldenLoc + "/instances/f1", "", "", "*filestore.Handler"},
		{s, g, goldenLoc + "/instances/r1", "", "", "*memorystore.Handler"},
		{s, g, goldenLoc + "/instances/d1", "", "", "*datafusion.Handler"},
		{s, g, goldenLoc + "/instances/s1", "", "", "*securesourcemanager.Handler"},
		{s, g, goldenLoc + "/instances/nope", "", "", "*memorystore.Handler"},
		{s, d, goldenLoc + "/instances/nope", "", "", "*memorystore.Handler"},
		{s, u, goldenLoc + "/instances/f1", "", `{"labels":{"a":"b"}}`, "*filestore.Handler"},
		{s, u, goldenLoc + "/instances/r1", "", `{"labels":{"a":"b"}}`, "*memorystore.Handler"},
		{s, p, goldenLoc + "/instances/d1:restart", "", `{}`, "*datafusion.Handler"},
		{s, p, goldenLoc + "/instances/nope:restart", "", `{}`, "*datafusion.Handler"},
		{s, p, goldenLoc + "/instances/r1:failover", "", `{}`, "*memorystore.Handler"},
		{s, g, goldenLoc + "/instances/s1:getIamPolicy", "", "", "*memorystore.Handler"},

		// repositories group: Secure Source Manager, Dataform, Artifact Registry.
		{e, g, goldenLoc + "/repositories", "", "", "*artifactregistry.Handler"},
		{e, p, goldenLoc + "/repositories?repositoryId=x", "", `{"format":"DOCKER"}`, "*artifactregistry.Handler"},
		{e, p, goldenLoc + "/repositories?repositoryId=x", "", `{"instance":"projects/demo/locations/us-central1/instances/s1"}`, "*securesourcemanager.Handler"},
		{e, p, goldenLoc + "/repositories?repositoryId=x", "", `{"gitRemoteSettings":{"url":"https://x"}}`, "*artifactregistry.Handler"},
		{e, g, goldenLoc + "/repositories/nope", "", "", "*artifactregistry.Handler"},
		{e, g, "/v1beta1/projects/demo/locations/us-central1/repositories", "", "", "*dataform.Handler"},

		// endpoints group: Cloud IDS, Vertex AI.
		{e, g, goldenLoc + "/endpoints", "", "", "*cloudids.Handler"},
		{e, p, goldenLoc + "/endpoints?endpointId=x", "", `{"severity":"INFORMATIONAL","network":"n"}`, "*cloudids.Handler"},
		{e, p, goldenLoc + "/endpoints", "", `{"displayName":"x"}`, "*cloudids.Handler"},
		{e, g, goldenLoc + "/endpoints/nope", "", "", "*cloudids.Handler"},
		{e, p, goldenLoc + "/endpoints/111:predict", "", `{}`, "*cloudids.Handler"},
		{e, p, goldenLoc + "/publishers/google/models/gemini:generateContent", "", `{}`, "*firestore.Handler"},
		{e, g, goldenLoc + "/models", "", "", "*vertexai.Handler"},
		{e, g, goldenLoc + "/datasets", "", "", "*vertexai.Handler"},

		// backupPlans group: GKE Backup, Backup and DR.
		{e, g, goldenLoc + "/backupPlans", "", "", "*gkebackup.Handler"},
		{e, p, goldenLoc + "/backupPlans?backupPlanId=x", "", `{"cluster":"projects/demo/locations/us-central1/clusters/c1"}`, "*gkebackup.Handler"},
		{e, p, goldenLoc + "/backupPlans?backupPlanId=x", "", `{"backupVault":"v","resourceType":"compute.googleapis.com/Instance"}`, "*gkebackup.Handler"},
		{e, g, goldenLoc + "/backupVaults", "", "", "*backupdr.Handler"},

		// Spanner and Cloud SQL v1 share /v1/projects/{p}/instances.
		{e, g, "/v1/projects/demo/instances", "", "", "*spanner.Handler"},
		{e, g, "/v1/projects/demo/instances/i1", "", "", "*cloudsql.Handler"},
		{e, g, "/v1/projects/demo/instances/i1/databases", "", "", "*cloudsql.Handler"},
		{e, g, "/v1/projects/demo/instances/i1/operations", "", "", "*cloudsql.Handler"},
		{e, g, "/v1/projects/demo/instances/i1/databases/db1/sessions", "", "", "*cloudsql.Handler"},
		{e, g, "/v1/projects/demo/instanceConfigs", "", "", "*firestore.Handler"},

		// Other /v1/projects/ handlers.
		{e, g, "/v1/projects/demo/topics", "", "", "*pubsub.Handler"},
		{e, g, "/v1/projects/demo/subscriptions", "", "", "*pubsub.Handler"},
		{e, g, "/v1/projects/demo/secrets", "", "", "*secretmanager.Handler"},
		{e, g, "/v1/projects/demo/serviceAccounts", "", "", "*iam.Handler"},
		{e, g, goldenLoc + "/functions", "", "", "*cloudfunctions.Handler"},
		{e, g, goldenLoc + "/services", "", "", "*metastore.Handler"},
		{e, g, goldenLoc + "/jobs", "", "", "*scheduler.Handler"},
		{e, g, goldenLoc + "/keyRings", "", "", "*kms.Handler"},
		{e, g, goldenLoc + "/queues", "", "", "*firestore.Handler"},
		{e, g, goldenLoc + "/workflows", "", "", "*workflows.Handler"},
		{e, g, goldenLoc + "/environments", "", "", "*composer.Handler"},
		{e, g, goldenLoc + "/connectors", "", "", "*vpcaccess.Handler"},
		{e, g, goldenLoc + "/triggers", "", "", "*eventarc.Handler"},
		{e, g, goldenLoc + "/namespaces", "", "", "*servicedirectory.Handler"},
		{e, g, goldenLoc + "/memberships", "", "", "*gkehub.Handler"},
		{e, g, goldenLoc + "/lakes", "", "", "*dataplex.Handler"},
		{e, g, goldenLoc + "/streams", "", "", "*datastream.Handler"},
		{e, g, goldenLoc + "/certificates", "", "", "*certificatemanager.Handler"},
		{e, g, goldenLoc + "/caPools", "", "", "*privateca.Handler"},
		{e, g, goldenLoc + "/deliveryPipelines", "", "", "*clouddeploy.Handler"},
		{e, g, goldenLoc + "/apis", "", "", "*apigateway.Handler"},
		{e, g, goldenLoc + "/clusters/c1:getIamPolicy", "", "", "*gke.Handler"},
		{e, g, "/v1/projects/demo/locations/global/hubs", "", "", "*networkconnectivity.Handler"},
		{e, g, "/v1/projects/demo/locations/us-central1/regions/us-central1/clusters", "", "", "*firestore.Handler"},
		{e, g, "/v1/projects/demo/databases/(default)/documents/c", "", "", "*firestore.Handler"},
		{e, g, "/v1/projects/demo/databases", "", "", "*firestore.AdminHandler"},
		{e, g, "/v1/projects/demo", "", "", "*firestore.Handler"},
		{e, g, "/v1/projects/demo/locations", "", "", "*firestore.Handler"},
		{e, g, "/v1/projects/demo/policy", "", "", "*binaryauthorization.Handler"},
		{e, g, "/v1/projects/demo/logs", "", "", "*firestore.Handler"},
		{e, g, "/v1/entries:list", "", "", "<nil>"},
		{e, g, "/v1/billingAccounts", "", "", "*cloudbilling.Handler"},

		// Non-/v1 roots.
		{e, g, "/v2/projects/demo/locations/us-central1/services", "", "", "*cloudrun.Handler"},
		{e, g, "/v3/projects/demo", "", "", "<nil>"},
		{e, g, "/v3/projects/demo/timeSeries", "", "", "*monitoring.Handler"},
		{e, g, "/bigquery/v2/projects/demo/datasets", "", "", "*bigquery.Handler"},
		{e, g, "/sql/v1beta4/projects/demo/instances", "", "", "*cloudsql.Handler"},
		{e, g, "/storage/v1/b?project=demo", "", "", "*gcs.Handler"},
		{e, g, "/storage/v1/b/bkt/o", "", "", "*gcs.Handler"},
		{e, p, "/upload/storage/v1/b/bkt/o?uploadType=media&name=x", "", "x", "*gcs.Handler"},
		{e, g, "/download/storage/v1/b/bkt/o/x", "", "", "<nil>"},
		{e, g, "/bkt/obj", "", "", "*gcs.Handler"},
		{e, g, "/compute/v1/projects/demo/zones/us-central1-a/instances", "", "", "*compute.Handler"},
		{e, g, "/compute/v1/projects/demo/global/networks", "", "", "*vpc.Handler"},
		{e, g, "/dns/v1/projects/demo/managedZones", "", "", "*clouddns.Handler"},
		{e, g, "/_cloudemu/snapshot", "", "", "<nil>"},
		{e, p, "/_cloudemu/reset", "", "", "<nil>"},
		{e, g, "/", "", "", "<nil>"},
		// API hint: Host header and /<api>.googleapis.com/ path alias.
		{s, g, goldenLoc + "/clusters", "managedkafka.googleapis.com", "", "*managedkafka.Handler"},
		{s, g, "/managedkafka.googleapis.com" + goldenLoc + "/clusters", "", "", "*managedkafka.Handler"},
		{s, g, goldenLoc + "/clusters", "container.googleapis.com", "", "*gke.Handler"},
		{s, g, "/container.googleapis.com" + goldenWest + "/clusters", "", "", "*gke.Handler"},
		{s, g, "/container.googleapis.com" + goldenLoc + "/clusters/k3", "", "", "*gke.Handler"},
		{s, g, "/managedkafka.googleapis.com" + goldenLoc + "/clusters/c1", "", "", "*managedkafka.Handler"},
		{s, g, "/managedkafka.googleapis.com" + goldenLoc + "/clusters/k3/consumerGroups", "", "", "*firestore.Handler"},
		{s, g, "/alloydb.googleapis.com" + goldenLoc + "/clusters", "", "", "*firestore.Handler"},
		{s, g, goldenLoc + "/clusters", "us-central1-aiplatform.googleapis.com", "", "*gke.Handler"},
		{s, g, goldenLoc + "/clusters", "www.googleapis.com", "", "*gke.Handler"},
		{e, g, "/managedkafka.googleapis.com" + goldenLoc + "/operations/nope", "", "", "*lro.Handler"},
		{s, g, goldenLoc + "/instances", "redis.googleapis.com", "", "*memorystore.Handler"},
		{s, g, "/redis.googleapis.com" + goldenLoc + "/instances", "", "", "*memorystore.Handler"},
		{s, g, "/file.googleapis.com" + goldenLoc + "/instances", "", "", "*filestore.Handler"},
		{s, g, "/datafusion.googleapis.com" + goldenLoc + "/instances", "", "", "*datafusion.Handler"},
		{s, g, "/securesourcemanager.googleapis.com" + goldenLoc + "/instances", "", "", "*securesourcemanager.Handler"},
		{s, d, goldenLoc + "/instances/nope", "datafusion.googleapis.com", "", "*datafusion.Handler"},
		{s, d, "/datafusion.googleapis.com" + goldenLoc + "/instances/nope", "", "", "*datafusion.Handler"},
		{s, g, "/redis.googleapis.com" + goldenLoc + "/instances/f1", "", "", "*memorystore.Handler"},
		{s, g, "/file.googleapis.com" + goldenLoc + "/instances/r1", "", "", "*filestore.Handler"},
		{e, p, "/file.googleapis.com" + goldenLoc + "/instances?instanceId=x", "", `{"tier":"NOPE"}`, "*filestore.Handler"},
		{e, p, "/securesourcemanager.googleapis.com" + goldenLoc + "/repositories?repositoryId=x", "", `{}`, "*securesourcemanager.Handler"},
		{e, g, "/artifactregistry.googleapis.com" + goldenLoc + "/repositories", "", "", "*artifactregistry.Handler"},
		{e, g, "/storage.googleapis.com/storage/v1/b?project=demo", "", "", "*gcs.Handler"},
		{e, g, "/secretmanager.googleapis.com/v1/projects/demo/secrets", "", "", "*secretmanager.Handler"},
		{e, g, "/storage/v1/b?project=demo", "storage.googleapis.com", "", "*gcs.Handler"},
		{e, g, "/_cloudemu/snapshot", "container.googleapis.com", "", "<nil>"},
	}
}

func goldenServers(t *testing.T) map[string]*server.Server {
	t.Helper()

	seeded := gcpserver.New(gcpserver.DriversFrom(cloudemu.NewGCP()))
	goldenSeed(t, seeded)

	return map[string]*server.Server{
		"empty":  gcpserver.New(gcpserver.DriversFrom(cloudemu.NewGCP())),
		"seeded": seeded,
	}
}

// TestRoutingGolden pins which handler type the assembled GCP server picks for
// each request shape.
func TestRoutingGolden(t *testing.T) {
	servers := goldenServers(t)

	for _, row := range goldenRows() {
		name := row.scenario + " " + row.method + " " + row.host + row.path

		var body *strings.Reader
		if row.body != "" {
			body = strings.NewReader(row.body)
		}

		req := httptest.NewRequest(row.method, row.path, nil)
		if body != nil {
			req = httptest.NewRequest(row.method, row.path, body)
		}

		if row.host != "" {
			req.Host = row.host
		}

		req, _ = sharedpath.Rewrite(nil, req)

		got := fmt.Sprintf("%T", servers[row.scenario].Match(req))
		if got != row.want {
			t.Errorf("%s: got %s, want %s", name, got, row.want)
		}
	}
}
