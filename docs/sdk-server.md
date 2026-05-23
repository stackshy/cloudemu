# SDK-Compatible HTTP Server

CloudEmu includes an HTTP server that speaks the real cloud SDK wire protocols across all three providers — AWS, Azure, and GCP. Point the actual `aws-sdk-go-v2`, `azure-sdk-for-go`, or `cloud.google.com/go` / `google.golang.org/api` clients at it (via custom endpoint) and your production code runs unchanged against the in-memory backend.

Nothing to mock. No Docker. No accounts. The same SDK calls you'd run against real AWS / Azure / GCP hit a local `httptest.NewServer` and get back SDK-decodable responses.

## Why

CloudEmu's Go API is great for new code you write for testing. But most real apps already use the official cloud SDKs directly. Rewriting those call sites just to test against an emulator is friction. The SDK-compat server removes that friction — change the endpoint, done.

## Quick start (AWS)

```go
import (
    "net/http/httptest"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/stackshy/cloudemu"
    awsserver "github.com/stackshy/cloudemu/server/aws"
)

cloud := cloudemu.NewAWS()
srv := awsserver.New(awsserver.Drivers{
    S3:         cloud.S3,
    DynamoDB:   cloud.DynamoDB,
    EC2:        cloud.EC2,
    VPC:        cloud.VPC,
    Lambda:     cloud.Lambda,
    SQS:        cloud.SQS,
    CloudWatch: cloud.CloudWatch,
    RDS:        cloud.RDS,
    Redshift:   cloud.Redshift,
    EKS:        cloud.EKS,
})
ts := httptest.NewServer(srv)
defer ts.Close()

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})

// Use the real SDK exactly as you would against AWS.
client.PutObject(ctx, &s3.PutObjectInput{...})
```

## Quick start (Azure)

```go
import (
    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
    "github.com/stackshy/cloudemu"
    azureserver "github.com/stackshy/cloudemu/server/azure"
)

cp := cloudemu.NewAzure()
srv := azureserver.New(azureserver.Drivers{
    VirtualMachines: cp.VirtualMachines,
    BlobStorage:     cp.BlobStorage,
    CosmosDB:        cp.CosmosDB,
    Network:         cp.VNet,
    Monitor:         cp.Monitor,
    Functions:       cp.Functions,
    ServiceBus:      cp.ServiceBus,
    SQL:             cp.SQL,
    PostgresFlex:    cp.PostgresFlex,
    MySQLFlex:       cp.MySQLFlex,
    AKS:             cp.AKS,
})
ts := httptest.NewTLSServer(srv) // Azure SDK requires TLS

opts := &arm.ClientOptions{
    ClientOptions: azcore.ClientOptions{
        Cloud: cloud.Configuration{
            Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
                cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
            },
        },
        Transport: ts.Client(),
    },
}
client, _ := armcompute.NewVirtualMachinesClient("sub-1", fakeCred{}, opts)
```

## Quick start (GCP)

```go
import (
    gcpcompute "cloud.google.com/go/compute/apiv1"
    "github.com/stackshy/cloudemu"
    gcpserver "github.com/stackshy/cloudemu/server/gcp"
    "google.golang.org/api/option"
)

cp := cloudemu.NewGCP()
srv := gcpserver.New(gcpserver.Drivers{
    Compute:        cp.GCE,
    Storage:        cp.GCS,
    Firestore:      cp.Firestore,
    Networking:     cp.VPC,
    Monitoring:     cp.CloudMonitoring,
    CloudFunctions: cp.CloudFunctions,
    PubSub:         cp.PubSub,
    CloudSQL:       cp.CloudSQL,
    GKE:            cp.GKE,
})
ts := httptest.NewServer(srv)

opts := []option.ClientOption{
    option.WithEndpoint(ts.URL),
    option.WithoutAuthentication(),
    option.WithHTTPClient(ts.Client()),
}
client, _ := gcpcompute.NewInstancesRESTClient(ctx, opts...)
```

Region, credentials, and tokens can be any dummy values — the server doesn't validate signatures or AAD tokens.

## Currently supported

### AWS (`server/aws/`)

| Service | Operations |
|---------|-----------|
| **S3** | CreateBucket, DeleteBucket, ListBuckets, PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2 (prefix, delimiter, common prefixes, continuation token), CopyObject |
| **DynamoDB** | CreateTable, DeleteTable, DescribeTable, ListTables, PutItem, GetItem, DeleteItem, UpdateItem (SET/REMOVE), Query, Scan (with FilterExpression), BatchWriteItem, BatchGetItem, TransactWriteItems |
| **EC2** | RunInstances, DescribeInstances (filters: `instance-id`, `instance-type`, `instance-state-name`, `tag:*`), Start/Stop/Reboot/TerminateInstances, ModifyInstanceAttribute |
| **EC2 — VPC + Networking** | VPCs, Subnets, Security Groups + ingress/egress rules, Internet Gateways, Route Tables + Routes, NAT Gateways, VPC Peering, Flow Logs, Network ACLs |
| **EC2 — EBS + Key Pairs** | Volumes (Create/Delete/Describe/Attach/Detach), Key Pairs |
| **EC2 — Snapshots + AMIs + Spot + Launch Templates** | Snapshots, Images, Spot instance requests, Launch Templates |
| **Auto Scaling** | CreateAutoScalingGroup, Update/Delete/Describe, SetDesiredCapacity, scaling policies |
| **Lambda** *(REST + JSON)* | CreateFunction, GetFunction, ListFunctions, DeleteFunction, Invoke (sync) |
| **SQS** *(JSON-RPC AwsJson1_0)* | CreateQueue, GetQueueUrl, ListQueues, DeleteQueue, SendMessage, ReceiveMessage, DeleteMessage |
| **CloudWatch** *(Smithy rpc-v2-cbor)* | PutMetricData, GetMetricStatistics, ListMetrics, PutMetricAlarm, DescribeAlarms, DeleteAlarms |
| **RDS / Aurora** *(query protocol)* | DBInstances (Create/Describe/Modify/Delete/Start/Stop/Reboot), DBClusters (Create/Describe/Modify/Delete/Start/Stop), DBSnapshots + DBClusterSnapshots (Create/Describe/Delete/Restore). One handler also serves the **Neptune** and **DocumentDB** engines — both reuse the same `aws-sdk-go-v2/service/{neptune,docdb}` client surface. |
| **Redshift** *(query protocol)* | CreateCluster, DescribeClusters, ModifyCluster, DeleteCluster, RebootCluster, CreateClusterSnapshot, DescribeClusterSnapshots, DeleteClusterSnapshot, RestoreFromClusterSnapshot |
| **EKS** *(REST + JSON)* | Clusters (Create/Describe/List/UpdateConfig/UpdateVersion/Delete), NodeGroups (Create/Describe/List/UpdateConfig/UpdateVersion/Delete), Fargate Profiles (Create/Describe/List/Delete), Addons (Create/Describe/List/Update/Delete). Stub kubeconfig only — data plane deferred to Wave 2. |

### Azure (`server/azure/`)

All handlers speak ARM JSON over HTTPS unless noted.

| Service | ARM provider / operations |
|---------|--------------------------|
| **Virtual Machines** | `Microsoft.Compute/virtualMachines` — CreateOrUpdate, Get, List, Delete, start, powerOff, restart |
| **Disks / Snapshots / Images / SSH Public Keys** | `Microsoft.Compute/{disks,snapshots,images,sshPublicKeys}` — full CRUD |
| **Blob Storage** *(data plane)* | Containers + Blobs: Create/Delete/List, PutBlob, GetBlob, DeleteBlob, CopyBlob |
| **Cosmos DB** *(data plane)* | Databases, Containers, Documents — full CRUD with `x-ms-documentdb-*` headers |
| **Virtual Network** | `Microsoft.Network/virtualNetworks` — CRUD + subnets |
| **Azure Monitor** | `microsoft.insights/metricAlerts` and metric data ingest/read |
| **Functions** | `Microsoft.Web/sites` (Function Apps): CreateOrUpdate, Get, List, Delete + non-ARM `/api/{name}` invoke |
| **Service Bus** | `Microsoft.ServiceBus/namespaces[/queues]` ARM CRUD + raw-HTTP REST data plane (`POST /{ns}/{queue}/messages`, `DELETE /messages/head`) |
| **SQL Database** | `Microsoft.Sql/servers[/databases]` — servers and databases, full CRUD lifecycle |
| **PostgreSQL Flexible Server** | `Microsoft.DBforPostgreSQL/flexibleServers` — full CRUD lifecycle |
| **MySQL Flexible Server** | `Microsoft.DBforMySQL/flexibleServers` — full CRUD lifecycle |
| **AKS** | `Microsoft.ContainerService/managedClusters` — ManagedClusters (CreateOrUpdate, Get, UpdateTags, Delete, List/ListByResourceGroup), AgentPools (CreateOrUpdate, Get, Delete, List), MaintenanceConfigurations (CreateOrUpdate, Get, Delete, List), ListClusterAdmin/User/MonitoringUser Credentials, RotateClusterCertificates. Stub kubeconfig only — data plane deferred to Wave 2. |

### GCP (`server/gcp/`)

All handlers speak REST + JSON.

| Service | Operations |
|---------|-----------|
| **Compute Engine** | Instances + Disks + Snapshots + Images: insert/get/list/delete with LRO envelopes |
| **Networks** | VPCs, Subnetworks, Firewalls, Routes |
| **Cloud Storage (GCS)** | Buckets + Objects: create/get/list/delete, upload, download, copy |
| **Firestore** | Documents + Collections via `:commit`, `:batchGet`, `:runQuery` |
| **Cloud Monitoring** | Time-series ingest/read, alert policies |
| **Cloud Functions v1** | Create (LRO), Get, List, Delete (LRO), `:call` (sync invoke) |
| **Pub/Sub** | Topics + Subscriptions lifecycle, `:publish`, `:pull`, `:acknowledge` |
| **Cloud SQL** | Instances (insert/get/list/patch/delete/start/stop/restart) + Operations (get/list) — supports the `sqladmin/v1` SDK |
| **GKE** | Clusters (Create/Get/List/Update/Delete + `:setLogging`/`:setMonitoring`/`:setMasterAuth`/`:setLegacyAbac`/`:setNetworkPolicy`/`:setMaintenancePolicy`/`:setResourceLabels`/`:startIpRotation`/`:completeIpRotation`), NodePools (Create/Get/List/Update/Delete + `:setSize`/`:setAutoscaling`/`:setManagement`/`:rollback`), Operations (Get/List/`:cancel`). Stub kubeconfig only — data plane deferred to Wave 2. |

Any operation not in these lists returns `501 Not Implemented` or the provider's native `UnknownOperation` / `NotImplemented` / `NOT_FOUND` error.

## How it's wired internally

The server is a tiny core plus a plugin-per-service model. Each service is a self-contained package under `server/`.

```
server/
├── server.go                       # core: Handler interface + Server (~80 LOC)
├── wire/
│   ├── wire.go                     # shared XML/JSON helpers
│   ├── awsquery/                   # AWS query-protocol form decoder + XML envelope
│   ├── azurearm/                   # ARM URL parser + JSON helpers + error envelope
│   └── gcprest/                    # GCP REST URL parser + Operation LRO helpers
├── aws/
│   ├── aws.go                      # awsserver.New(Drivers{...})
│   ├── s3/  ec2/  dynamodb/  lambda/  sqs/  cloudwatch/
│   ├── rds/  redshift/             # query-protocol relational DB handlers
│   └── eks/                        # REST EKS control-plane handler
├── azure/
│   ├── azure.go                    # azureserver.New(Drivers{...})
│   ├── virtualmachines/  disks/  snapshots/  images/  sshpublickeys/
│   ├── blob/  cosmos/  network/  monitor/  functions/  servicebus/
│   ├── azuresql/  postgresflex/  mysqlflex/   # ARM relational DB handlers
│   └── aks/                        # ARM AKS control-plane handler
└── gcp/
    ├── gcp.go                      # gcpserver.New(Drivers{...})
    ├── compute/  networks/  gcs/  firestore/  monitoring/
    ├── cloudfunctions/  pubsub/
    ├── cloudsql/                   # REST Cloud SQL handler
    └── gke/                        # REST GKE control-plane handler
```

Each handler implements a two-method interface:

```go
type Handler interface {
    Matches(r *http.Request) bool                    // detect by header/path/form
    ServeHTTP(w http.ResponseWriter, r *http.Request)
}
```

`server.Server` iterates registered handlers and dispatches to the first that claims the request. Adding a new service is one new package + one `Register` call. The core never changes.

### Protocol detection

Each handler uses a different signal so dispatch is unambiguous within a provider:

| Handler | How it's detected |
|---------|-------------------|
| AWS DynamoDB | `X-Amz-Target: DynamoDB_20120810.*` header |
| AWS SQS | `X-Amz-Target: AmazonSQS.*` header |
| AWS Lambda | URL prefix `/2015-03-31/functions` |
| AWS EKS | URL prefix `/clusters` |
| AWS RDS | Form-encoded POST whose `Action=` is a known RDS operation (registered before EC2) |
| AWS Redshift | Form-encoded POST whose `Action=` is a known Redshift operation (registered before EC2) |
| AWS EC2 | `Action=…` in URL query or `Content-Type: application/x-www-form-urlencoded` POST |
| AWS CloudWatch | `Smithy-Protocol: rpc-v2-cbor` header |
| AWS S3 | Fallback (everything else REST-shaped) |
| Azure (all ARM) | URL begins with `/subscriptions/{sub}` and matches `Microsoft.<Provider>/<Type>` |
| Azure SQL | ARM provider `Microsoft.Sql` |
| Azure PostgreSQL Flexible | ARM provider `Microsoft.DBforPostgreSQL/flexibleServers` |
| Azure MySQL Flexible | ARM provider `Microsoft.DBforMySQL/flexibleServers` |
| Azure AKS | ARM provider `Microsoft.ContainerService/managedClusters` |
| Azure Cosmos | URL begins with `/dbs/` (data plane, non-ARM) |
| Azure Functions invoke | URL begins with `/api/` (non-ARM data plane) |
| Azure Service Bus data plane | Non-ARM URL ending in `/messages` or `/messages/head` |
| Azure Blob | Fallback (everything else non-ARM that's REST-shaped) |
| GCP Compute / Networks | URL prefix `/compute/v1/` |
| GCP Cloud Functions | `/v1/projects/.../locations/.../functions[/...]` |
| GCP Pub/Sub | `/v1/projects/.../topics[/...]` or `/v1/projects/.../subscriptions[/...]` |
| GCP Firestore | `/v1/projects/.../databases/.../documents[/...]` |
| GCP Cloud Monitoring | `/v3/projects/.../` |
| GCP Cloud SQL | `/v1/projects/.../{instances,operations}[/...]` |
| GCP GKE | `/v1/projects/.../locations/.../{clusters,operations}[/...]` |
| GCP GCS | Fallback (`/storage/v1/` and `/{bucket}/{object}` direct-media) |
| **Kubernetes data plane** (shared across all 3 providers) | URL prefix `/k8s/{cluster-uid}/`. Registered on AWS, Azure, and GCP servers; cluster UID is the one minted by the matching control-plane handler on Create. |

Registration order matters when handlers share a path prefix — `awsserver.New` / `azureserver.New` / `gcpserver.New` register more-specific handlers ahead of catch-alls (S3, Blob, GCS) so first-match-wins resolves correctly.

## Coverage status

| Provider | Domains shipped | Notes |
|----------|----------------|-------|
| AWS | Storage, Compute (+ VPC/SG/Subnet/IGW/RT/NAT/Peering/FlowLogs/NACL/EBS/Keys/AMIs/Snapshots/Spot/LaunchTemplates), NoSQL DB, Relational DB (RDS/Aurora/Neptune/DocumentDB/Redshift), Kubernetes (EKS control plane + shared data plane), Serverless, Message Queue, Monitoring | The most-mature provider — EC2 was Phase 1 of SDK-compat |
| Azure | Storage, Compute (+ Disks/Snapshots/Images/SSHKeys), NoSQL DB, Relational DB (SQL Database, PostgreSQL Flexible Server, MySQL Flexible Server), Kubernetes (AKS control plane + shared data plane), Serverless, Message Queue (ARM only), Networking, Monitoring | Data-plane Service Bus over AMQP is out of scope (use raw-HTTP REST data plane for tests) |
| GCP | Storage, Compute (+ Disks/Snapshots/Images), NoSQL DB, Relational DB (Cloud SQL), Kubernetes (GKE control plane + shared data plane), Serverless, Message Queue, Networking, Monitoring | All driven via REST (the `cloud.google.com/go/*` clients with `option.WithEndpoint`, or the auto-generated `google.golang.org/api/*` clients) |

Kubernetes ships as **two cooperating handlers**: per-provider control planes (EKS / AKS / GKE — clusters + node pools + addons / Fargate / maintenance configs) and a shared in-memory **data plane** registered under `/k8s/{cluster-uid}/`. The control plane mints a UID on every cluster Create and embeds it in the kubeconfig (or `Cluster.Endpoint` for GKE); `client-go` and `kubectl` connect to the data plane via that URL. Resources served on the data plane: Namespace, ConfigMap, Pod, Service, Secret, ServiceAccount, Deployment (apps/v1), and Endpoints (auto-created for each Service). Every list endpoint supports `?watch=true` for streaming events, so real `Informer` / `Reflector` machinery works.

The data plane intentionally has no controllers — Deployments don't spawn ReplicaSets, Pods stay Pending, Endpoints are empty stubs. RBAC, subresources, PV/PVC, StatefulSet/DaemonSet/Job/CronJob, and Ingress are out of scope.

The remaining service domains (IAM, DNS, Load Balancer, Cache, Secrets, Logging, Notifications, Container Registry, Event Bus) have full driver implementations in `providers/{aws,azure,gcp}/`; SDK-compat handlers are added in lockstep across all 3 providers as each domain ships.

## Writing your own handler

If you need a service we don't cover yet, implement the `server.Handler` interface in your own package and register it:

```go
type MyHandler struct{ /* driver */ }

func (*MyHandler) Matches(r *http.Request) bool {
    // your detection logic
}

func (h *MyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // your logic
}

srv := server.New()
srv.Register(&MyHandler{...})
```

The `Handler` interface is the only contract — no registration is needed in core CloudEmu. If the handler is generally useful, a PR to add it under `server/<provider>/<service>` is welcome.

## Limitations

- **No signature validation.** CloudEmu is a local development tool, not a security boundary. Requests are accepted regardless of AWS SigV4 / Azure AAD / GCP OAuth signatures.
- **No AMQP for Azure Service Bus.** The modern `azservicebus` SDK uses AMQP exclusively for data plane. ARM control plane is fully supported via `armservicebus`; tests that need send/receive can use the raw-HTTP REST data plane.
- **GCS direct-media downloads** assume path-style URLs.
- **DynamoDB / Cosmos / Firestore filters and queries** support common patterns but are not full DSL parsers.
- **Pagination tokens** are honored where present in the SDK contract; some list operations short-circuit to a single page.

When a client hits an unsupported operation, the server responds with the provider's native error code so failures are easy to diagnose.
