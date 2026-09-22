# SDK-Compatible HTTP Server

CloudEmu includes an HTTP server that speaks the AWS, Azure and GCP SDK wire protocols. Point the real `aws-sdk-go-v2`, `azure-sdk-for-go`, or `cloud.google.com/go` / `google.golang.org/api` clients at it with a custom endpoint, and your production code runs unchanged against the in-memory backend. The server runs in a local `httptest.NewServer`, so you don't need Docker or a cloud account, and the responses decode with the normal SDK types.

The backend is in-memory by default. If you need real workloads behind the wire protocol, the same drivers can be backed by opt-in [real engines](features.md#11-real-data-plane-engines-opt-in) (real SQL, Redis or function code).

> This page covers library mode: the in-process SDK-compat server for Go unit tests, built with `httptest.NewServer`. To use cloudemu with an application that is already running, don't start it from a `_test.go` file. Run [server mode](standalone-server.md) and set your SDK's endpoint (`AWS_ENDPOINT_URL` / `o.BaseEndpoint`, `option.WithEndpoint`, or the Azure ARM endpoint override) as described in [integration.md](integration.md). Server mode is the default for integration and E2E tests. The wire protocol and service coverage below are the same in both modes; only the way you start the server differs.

## Why

Most apps call the official cloud SDKs directly. Rewriting those call sites to use CloudEmu's Go API just for tests is extra work. With the SDK-compat server you only change the endpoint.

## Quick start (AWS)

```go
import (
    "net/http/httptest"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/stackshy/cloudemu/v2"
    awsserver "github.com/stackshy/cloudemu/v2/server/aws"
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
    "github.com/stackshy/cloudemu/v2"
    azureserver "github.com/stackshy/cloudemu/v2/server/azure"
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
    "github.com/stackshy/cloudemu/v2"
    gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
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

## Quick start (Databricks)

The Azure server also speaks the `databricks-sdk-go` `WorkspaceClient` wire protocol. Wire the same `*databricks.Mock` into both `Databricks` (ARM workspace control plane) and `DatabricksDataPlane` (the `/api/2.x` workspace data plane).

```go
import (
    "net/http/httptest"

    databricks "github.com/databricks/databricks-sdk-go"
    "github.com/databricks/databricks-sdk-go/config"
    "github.com/databricks/databricks-sdk-go/service/compute"
    "github.com/stackshy/cloudemu/v2"
    azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

cp := cloudemu.NewAzure()
srv := azureserver.New(azureserver.Drivers{
    Databricks:          cp.Databricks, // Microsoft.Databricks/workspaces (ARM)
    DatabricksDataPlane: cp.Databricks, // /api/2.x workspace data plane
})
ts := httptest.NewServer(srv)
defer ts.Close()

w, _ := databricks.NewWorkspaceClient(&databricks.Config{
    Host:        ts.URL,
    Token:       "test-token",
    Credentials: config.PatCredentials{},
})

// Real WorkspaceClient calls round-trip against the in-memory backend.
w.InstancePools.Create(ctx, compute.CreateInstancePool{InstancePoolName: "pool-1"})
```

Region, credentials and tokens can be any dummy values. By default the server doesn't validate signatures or AAD tokens (see [Limitations](#limitations)).

## Supported operations

The tables below list the main handlers. They are not the full list: the generated [capability coverage](coverage/README.md) has every service and operation.

### AWS (`server/aws/`)

| Service | Operations |
|---------|-----------|
| **S3** | CreateBucket, DeleteBucket, ListBuckets, PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2 (prefix, delimiter, common prefixes, continuation token), CopyObject |
| **DynamoDB** | CreateTable, DeleteTable, DescribeTable, ListTables, PutItem, GetItem, DeleteItem, UpdateItem (SET/REMOVE/ADD/DELETE + arithmetic/`if_not_exists`/`list_append`), Query/Scan (full KeyCondition/Filter/Projection expressions), BatchWriteItem, BatchGetItem, TransactWriteItems (ConditionExpression, real `SS`/`NS`/`BS` sets) |
| **EC2** | RunInstances, DescribeInstances (filters: `instance-id`, `instance-type`, `instance-state-name`, `tag:*`), Start/Stop/Reboot/TerminateInstances, ModifyInstanceAttribute |
| **EC2: VPC + Networking** | VPCs, Subnets, Security Groups + ingress/egress rules, Internet Gateways, Route Tables + Routes, NAT Gateways, VPC Peering, Flow Logs, Network ACLs |
| **EC2: EBS + Key Pairs** | Volumes (Create/Delete/Describe/Attach/Detach), Key Pairs |
| **EC2: Snapshots + AMIs + Spot + Launch Templates** | Snapshots, Images, Spot instance requests, Launch Templates |
| **Auto Scaling** | CreateAutoScalingGroup, Update/Delete/Describe, SetDesiredCapacity, scaling policies |
| **Lambda** *(REST + JSON)* | CreateFunction, GetFunction, ListFunctions, DeleteFunction, Invoke (sync) |
| **SQS** *(JSON-RPC AwsJson1_0)* | CreateQueue, GetQueueUrl, ListQueues, DeleteQueue, SendMessage, ReceiveMessage, DeleteMessage |
| **CloudWatch** *(Smithy rpc-v2-cbor)* | PutMetricData, GetMetricStatistics, ListMetrics, PutMetricAlarm, DescribeAlarms, DeleteAlarms |
| **RDS / Aurora** *(query protocol)* | DBInstances (Create/Describe/Modify/Delete/Start/Stop/Reboot), DBClusters (Create/Describe/Modify/Delete/Start/Stop), DBSnapshots + DBClusterSnapshots (Create/Describe/Delete/Restore). One handler also serves the **Neptune** and **DocumentDB** engines: both reuse the same `aws-sdk-go-v2/service/{neptune,docdb}` client surface. |
| **Redshift** *(query protocol)* | CreateCluster, DescribeClusters, ModifyCluster, DeleteCluster, RebootCluster, CreateClusterSnapshot, DescribeClusterSnapshots, DeleteClusterSnapshot, RestoreFromClusterSnapshot |
| **MemoryDB** *(JSON 1.1, `AmazonMemoryDB.*`)* | Clusters (Create/Describe/Update/Delete/FailoverShard/ListAllowedNodeTypeUpdates), ACLs & Users, Parameter Groups, Subnet Groups, Snapshots (Create/Describe/Copy/Delete + restore), tags, engine-version & event catalogs. Optional (type-asserted): Multi-Region clusters, Reserved Nodes. Server-side `MaxResults`/`NextToken` pagination on every Describe. |
| **Keyspaces** *(JSON 1.0, `KeyspacesService.*`)* | Keyspaces (Create/Get/List/Update/Delete, single/multi-region replication), Tables (Create/Get/List/Update/Delete/Restore: full schema, capacity, encryption, PITR, TTL, CDC), user-defined types, tags. Optional: `GetTableAutoScalingSettings`. Responses use lower-camel keys so the case-sensitive SDK deserializer decodes them; pagination on every list. |
| **EKS** *(REST + JSON)* | Clusters (Create/Describe/List/UpdateConfig/UpdateVersion/Delete), NodeGroups (Create/Describe/List/UpdateConfig/UpdateVersion/Delete), Fargate Profiles (Create/Describe/List/Delete), Addons (Create/Describe/List/Update/Delete). The kubeconfig points at the shared [Kubernetes data plane](#kubernetes). |
| **IAM** *(query protocol)* | Users (Create/Get/List/Delete), Roles (Create/Get/List/Delete), Policies (Create/Get/List/Delete), Attach/Detach/ListAttached for both Users and Roles, Groups (Create/Get/List/Delete + AddUserToGroup/RemoveUserFromGroup/ListGroupsForUser), AccessKeys (Create/List/Delete), InstanceProfiles (Create/Get/List/Delete + AddRoleToInstanceProfile/RemoveRoleFromInstanceProfile). Errors surface as typed `*types.NoSuchEntityException` / `*types.EntityAlreadyExistsException`. |
| **Resource Explorer 2** *(JSON)* | Search: free-text plus filter expression over the cross-service inventory; results include ARN, resource type, region, owning account, and tags |
| **Resource Groups Tagging API** *(JSON-RPC)* | GetResources (filter by `ResourceTypeFilters` + `TagFilters`, paginated), TagResources, UntagResources, GetTagKeys, GetTagValues |
| **Bedrock** *(REST + JSON, `bedrock` + `bedrock-runtime`)* | Control plane: foundation models (List/Get), model-customization jobs (Create/Get/List), custom models (List/Get/Delete), Guardrails (Create/Get/List/Update/Delete + CreateGuardrailVersion, with topic/content/word/sensitive-info/contextual-grounding policy configs and version snapshots), Provisioned Throughput (Create/Get/List/Delete), invocation-logging config (Put/Get/Delete), resource tagging (Tag/Untag/ListTagsForResource), model import jobs, model copy jobs, evaluation jobs (Create/Get/List/Stop), inference profiles (Create/Get/List/Delete), prompt routers (Create/Get/List/Delete), marketplace model endpoints (Create/Get/List/Update/Delete/Register/Deregister), foundation-model agreements (Create/Delete/ListOffers/GetAvailability), automated-reasoning policies (Create/Get/List/Update/Delete). Runtime: InvokeModel (family-aware response envelopes), Converse, ConverseStream + InvokeModelWithResponseStream (eventstream), CountTokens, ApplyGuardrail, and async invoke (Start/Get/List). |
| **Bedrock Agent** *(REST + JSON, `bedrock-agent` + `bedrock-agent-runtime`)* | Control plane: agents (Create/Get/List/Update/Delete/Prepare + alias), knowledge bases (CRUD), data sources (CRUD + StartIngestionJob), flows (CRUD + Prepare), prompts (CRUD). Runtime: InvokeAgent (eventstream), Retrieve, RetrieveAndGenerate. Scope: the core resource lifecycle and runtime data plane above; **agent versioning/aliases beyond basic create, action groups, and agent collaborators are out of scope** for this iteration. |

### Azure (`server/azure/`)

All handlers speak ARM JSON over HTTPS unless noted.

| Service | ARM provider / operations |
|---------|--------------------------|
| **Virtual Machines** | `Microsoft.Compute/virtualMachines`: CreateOrUpdate, Get, List, Delete, start, powerOff, restart |
| **Disks / Snapshots / Images / SSH Public Keys** | `Microsoft.Compute/{disks,snapshots,images,sshPublicKeys}`: full CRUD |
| **Blob Storage** *(data plane)* | Containers + Blobs: Create/Delete/List, PutBlob, GetBlob, DeleteBlob, CopyBlob |
| **Cosmos DB** *(data plane)* | Databases, Containers, Documents: full CRUD with `x-ms-documentdb-*` headers |
| **Cosmos DB (SQL ARM control plane)** | `Microsoft.DocumentDB/databaseAccounts/{acct}/sqlDatabases[/containers]`: SQL databases (CreateUpdate/Get/List/Delete, cascading container delete), containers (CreateUpdate/Get/List/Delete with partitionKey, defaultTtl, uniqueKeyPolicy, indexingPolicy), and `throughputSettings/default` at database and container level (Get/Update manual RU/s or autoscale maxThroughput + migrateToAutoscale/migrateToManualThroughput). Real `armcosmos` `SQLResources` clients round-trip end-to-end, including the LRO pollers, so Terraform/Bicep/`az cosmosdb sql` can manage the data model. Shares state with the Cosmos data plane above; a control-plane database/container/throughput is visible to data-plane clients and vice versa. |
| **Virtual Network** | `Microsoft.Network/{virtualNetworks,networkSecurityGroups,publicIPAddresses,networkInterfaces}`: CRUD + nested subnets; NICs bind a subnet and get a private IP |
| **Azure Monitor** | `microsoft.insights/metricAlerts` and metric data ingest/read |
| **Functions** | `Microsoft.Web/sites` (Function Apps): CreateOrUpdate, Get, List, Delete + non-ARM `/api/{name}` invoke |
| **Service Bus** | `Microsoft.ServiceBus/namespaces[/queues]` ARM CRUD + raw-HTTP REST data plane (`POST /{ns}/{queue}/messages`, `DELETE /messages/head`) |
| **SQL Database** | `Microsoft.Sql/servers[/databases]`: servers and databases, full CRUD lifecycle |
| **Managed Cassandra** | `Microsoft.DocumentDB/cassandraClusters[/dataCenters]`: clusters (CreateOrUpdate, Get, ListByResourceGroup, ListBySubscription, Update, Delete, deallocate, start, invokeCommand, status) and datacenters (CreateOrUpdate, Get, List, Update, Delete). Real `armcosmos` `CassandraClusters`/`CassandraDataCenters` clients round-trip end-to-end, including the LRO pollers. |
| **PostgreSQL Flexible Server** | `Microsoft.DBforPostgreSQL/flexibleServers`: full CRUD lifecycle |
| **Cosmos DB for PostgreSQL** | `Microsoft.DBforPostgreSQL/serverGroupsv2`: clusters (CreateOrUpdate, Get, ListByResourceGroup, ListBySubscription, Update, Delete, restart, start, stop, promote, checkNameAvailability), firewall rules, roles, derived servers/nodes, configurations (cluster/coordinator/node reads + updates), and private endpoint connections/links. Real `armcosmosforpostgresql` clients round-trip end-to-end, including the LRO pollers. |
| **MySQL Flexible Server** | `Microsoft.DBforMySQL/flexibleServers`: full CRUD lifecycle |
| **AKS** | `Microsoft.ContainerService/managedClusters`: ManagedClusters (CreateOrUpdate, Get, UpdateTags, Delete, List/ListByResourceGroup), AgentPools (CreateOrUpdate, Get, Delete, List), MaintenanceConfigurations (CreateOrUpdate, Get, Delete, List), ListClusterAdmin/User/MonitoringUser Credentials, RotateClusterCertificates. The kubeconfig points at the shared [Kubernetes data plane](#kubernetes). |
| **IAM (armauthorization)** | `Microsoft.Authorization`: RoleDefinitions (CreateOrUpdate, Get, List, Delete) and RoleAssignments (Create, Get, ListForScope, Delete) at any scope (subscription, resource group, resource, management group). Real `armauthorization` SDK clients round-trip end-to-end. Microsoft Graph (users/groups) is not implemented yet. |
| **Resource Graph** | `Microsoft.ResourceGraph`: `POST /providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01` with a KQL-shaped query over the cross-service inventory; supports `subscriptions[]` scoping and `$top`/`$skipToken` pagination. Rows carry the fixed columns (`id` [ARM-shaped], `name`, `type`, `location`, `resourceGroup`, `subscriptionId`, `tags`) plus resource-shape columns emitted when present; `sku.name`, `properties`, `managedBy`, `kind`, `zones`; so SKU/tier/size-sensitive consumers (e.g. a discovery + cost engine) can read a VM's size, a managed disk's tier/`diskSizeGB`/owning VM, or a flexible server's compute SKU. `project`/`summarize`/`join` are tolerated but ignored (the full row is always returned). |
| **Databricks (ARM control plane)** | `Microsoft.Databricks/workspaces`: CreateOrUpdate, Get, Delete, UpdateTags, List / ListByResourceGroup. Real `armdatabricks` SDK clients round-trip end-to-end. |
| **Databricks (workspace data plane)** *(`databricks-sdk-go`, `/api/2.x`)* | Point the real `WorkspaceClient` at `Config.Host`. Clusters (create/edit/start/restart/resize/pin/unpin/delete + list-node-types / spark-versions / zones), instance pools, jobs + runs (submit / run-now / get / list / cancel / cancel-all / repair / output / delete), cluster policies, libraries (install / uninstall / status), and object permissions. Self-contained families: secrets (scopes / secrets / ACLs), tokens, git credentials, repos, DBFS (incl. block upload), workspace notebooks/directories, SQL warehouses, pipelines, serving endpoints, SCIM identity (users / groups / service principals), and Unity Catalog (catalogs / schemas / tables + metastores / external locations / storage credentials / volumes). Also serves `GET /.well-known/databricks-config` so the SDK's host-metadata resolution succeeds (workspace-host stub) instead of logging a warning. |

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
| **Cloud SQL** | Instances (insert/get/list/patch/delete/start/stop/restart) + Operations (get/list): supports the `sqladmin/v1` SDK |
| **Bigtable** *(`bigtableadmin/v2`, `/v2`)* | Instances (create/get/list/update/partialUpdate/delete), Clusters (create/get/list/update/delete + getMemoryLayer), Tables (create/get/list/delete/undelete/modifyColumnFamilies/dropRowRange/generateConsistencyToken/checkConsistency/restore/patch), App Profiles (CRUD), Backups (create/get/list/patch/delete/copy), Operations (get), and per-resource IAM (get/set/testIamPermissions on instances/tables/backups). LROs return `Operation{done:true}` with the resource inline. |
| **GKE** | Clusters (Create/Get/List/Update/Delete + `:setLogging`/`:setMonitoring`/`:setMasterAuth`/`:setLegacyAbac`/`:setNetworkPolicy`/`:setMaintenancePolicy`/`:setResourceLabels`/`:startIpRotation`/`:completeIpRotation`), NodePools (Create/Get/List/Update/Delete + `:setSize`/`:setAutoscaling`/`:setManagement`/`:rollback`), Operations (Get/List/`:cancel`). The cluster endpoint points at the shared [Kubernetes data plane](#kubernetes). |
| **Cloud Asset Inventory** | `assets.list` (filter by `assetTypes[]`), `searchAllResources` (query + asset-type filter), `searchAllIamPolicies` (returns empty; not implemented), `exportAssets` (sync; inline results in the returned Operation), `batchGetAssetsHistory`, Feeds (create/list/get/patch/delete), `operations.get`. Resource names returned as GCP-shaped `//service/path` URNs. |
| **IAM (iam.googleapis.com v1)** | ServiceAccounts (Create/Get/List/Delete/Patch), custom Roles (Create/Get/List/Delete/Patch), ServiceAccountKeys (Create/Get/List/Delete). Real `google.golang.org/api/iam/v1` clients round-trip end-to-end; errors surface as typed `*googleapi.Error`. Resource-level `getIamPolicy`/`setIamPolicy` bindings on individual GCP resources are out of scope. |

An operation cloudemu doesn't implement returns `501 Not Implemented` or the provider's native `UnknownOperation` / `NotImplemented` / `NOT_FOUND` error.

## How it's wired internally

The server is a small core with one plugin package per service under `server/`. The tree below shows a few of them.

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
│   ├── eks/                        # REST EKS control-plane handler
│   ├── bedrock/                    # REST Bedrock control plane + bedrock-runtime
│   ├── bedrockagent/               # REST bedrock-agent control plane
│   └── bedrockagentruntime/        # REST bedrock-agent-runtime data plane
├── azure/
│   ├── azure.go                    # azureserver.New(Drivers{...})
│   ├── virtualmachines/  disks/  snapshots/  images/  sshpublickeys/
│   ├── blob/  cosmos/  network/  monitor/  functions/  servicebus/
│   ├── sql/  postgresflex/  mysqlflex/   # ARM relational DB handlers
│   ├── aks/                        # ARM AKS control-plane handler
│   └── databricks/                 # ARM workspace + workspace data-plane families
│       ├── secrets/  token/  gitcredentials/  repos/  dbfs/  wsfs/
│       ├── sqlwarehouses/  pipelines/  serving/  scim/
│       └── unitycatalog/  ucstorage/
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

`server.Server` goes through the registered handlers in order and dispatches to the first one that claims the request. A new service is one new package plus one `Register` call; the core doesn't change.

### Protocol detection

Each handler matches on a different signal, so dispatch within a provider is unambiguous:

| Handler | How it's detected |
|---------|-------------------|
| AWS DynamoDB | `X-Amz-Target: DynamoDB_20120810.*` header |
| AWS SQS | `X-Amz-Target: AmazonSQS.*` header |
| AWS MemoryDB | `X-Amz-Target: AmazonMemoryDB.*` header |
| AWS Keyspaces | `X-Amz-Target: KeyspacesService.*` header |
| AWS Lambda | URL prefix `/2015-03-31/functions` |
| AWS EKS | URL prefix `/clusters` |
| AWS RDS | Form-encoded POST whose `Action=` is a known RDS operation (registered before EC2) |
| AWS Redshift | Form-encoded POST whose `Action=` is a known Redshift operation (registered before EC2) |
| AWS EC2 | `Action=…` in URL query or `Content-Type: application/x-www-form-urlencoded` POST |
| AWS CloudWatch | `Smithy-Protocol: rpc-v2-cbor` header |
| AWS Bedrock | URL prefix `/foundation-models`, `/model-customization-jobs`, `/custom-models`, `/guardrails`, `/provisioned-model-throughput`, `/logging/modelinvocations`, `/tagResource`, `/untagResource`, `/listTagsForResource`, `/model-import-jobs`, `/model-copy-jobs`, `/evaluation-jobs`, `/evaluation-job/`, `/inference-profiles`, `/prompt-routers`, `/marketplace-model/endpoints`, `/automated-reasoning-policies`, `/create-foundation-model-agreement`, `/delete-foundation-model-agreement`, `/list-foundation-model-agreement-offers/`, `/foundation-model-availability/`, or bedrock-runtime `/model/{id}/{invoke,converse,converse-stream,invoke-with-response-stream,count-tokens}`, `/guardrail/{id}/version/{version}/apply`, and `/async-invoke` |
| AWS Bedrock Agent | Control plane URL prefix `/agents`, `/knowledgebases`, `/flows`, `/prompts`; runtime (registered first, matched only on POST) `/agents/{id}/agentAliases/{a}/sessions/{s}/text` (InvokeAgent), `/knowledgebases/{id}/retrieve` (Retrieve), and `/retrieveAndGenerate` |
| AWS S3 | Fallback (everything else REST-shaped) |
| Azure (all ARM) | URL begins with `/subscriptions/{sub}` and matches `Microsoft.<Provider>/<Type>` |
| Azure SQL | ARM provider `Microsoft.Sql` |
| Azure Managed Cassandra | ARM provider `Microsoft.DocumentDB/cassandraClusters` |
| Azure PostgreSQL Flexible | ARM provider `Microsoft.DBforPostgreSQL/flexibleServers` |
| Azure Cosmos DB for PostgreSQL | ARM provider `Microsoft.DBforPostgreSQL/serverGroupsv2` |
| Azure MySQL Flexible | ARM provider `Microsoft.DBforMySQL/flexibleServers` |
| Azure AKS | ARM provider `Microsoft.ContainerService/managedClusters` |
| Azure Databricks (ARM) | ARM provider `Microsoft.Databricks/workspaces` |
| Azure Databricks (data plane) | Non-ARM URL prefix `/api/2.0/` or `/api/2.1/` (workspace data plane) |
| Azure Databricks (host metadata) | `GET /.well-known/databricks-config` |
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

Registration order matters when handlers share a path prefix. `awsserver.New`, `azureserver.New` and `gcpserver.New` register the more specific handlers before the catch-alls (S3, Blob, GCS), so the first match is the right one.

## Coverage status

This summarizes the handlers on this page. See [docs/coverage](coverage/README.md) for everything else.

| Provider | Domains shipped | Notes |
|----------|----------------|-------|
| AWS | Storage, Compute (+ VPC/SG/Subnet/IGW/RT/NAT/Peering/FlowLogs/NACL/EBS/Keys/AMIs/Snapshots/Spot/LaunchTemplates), NoSQL DB, Relational DB (RDS/Aurora/Neptune/DocumentDB/Redshift), Kubernetes (EKS control plane + shared data plane), Serverless, Message Queue, Monitoring | |
| Azure | Storage, Compute (+ Disks/Snapshots/Images/SSHKeys), NoSQL DB, Relational DB (SQL Database, PostgreSQL Flexible Server, MySQL Flexible Server), Kubernetes (AKS control plane + shared data plane), Serverless, Message Queue (ARM only), Networking, Monitoring | Data-plane Service Bus over AMQP is out of scope (use raw-HTTP REST data plane for tests) |
| GCP | Storage, Compute (+ Disks/Snapshots/Images), NoSQL DB, Relational DB (Cloud SQL), Kubernetes (GKE control plane + shared data plane), Serverless, Message Queue, Networking, Monitoring | All driven via REST (the `cloud.google.com/go/*` clients with `option.WithEndpoint`, or the auto-generated `google.golang.org/api/*` clients) |

### Kubernetes

Kubernetes is served by two handlers that work together: a control plane per provider (EKS / AKS / GKE: clusters, node pools, addons, Fargate, maintenance configs) and a shared in-memory data plane registered under `/k8s/{cluster-uid}/`. The control plane mints a UID on every cluster Create and embeds it in the kubeconfig (or `Cluster.Endpoint` for GKE), along with a CA that signs the data plane's serving cert. `client-go` and `kubectl` therefore connect over verified TLS.

The data plane behaves like a small cluster that is always converged, similar to minikube. A synchronous reconcile step runs on every write, so Deployments, ReplicaSets, StatefulSets and DaemonSets produce Running Pods, Services get Endpoints, PVCs bind, and Jobs complete. All of this happens immediately and deterministically, with no controller goroutines. The core, apps, batch, networking, rbac, storage, autoscaling, discovery and policy groups are served, with `/scale` and `/status` subresources, label/field selectors, and `?watch=true` streaming (selector-filtered), so real `Informer` / `Reflector` code works.

It also supports:

- CustomResourceDefinitions (dynamically served kinds)
- server-side apply with `managedFields` ownership and conflict detection
- `?dryRun=All`, finalizer-gated deletion, and `?limit=&continue=` pagination
- synthetic `pods/log` and PDB-gated `pods/eviction`
- `metrics.k8s.io` (`kubectl top`) and HPA scaling
- object-count ResourceQuota / LimitRange / PDB enforcement
- RBAC SubjectAccessReview and NetworkPolicy evaluation
- opt-in admission webhooks
- watch resume from `resourceVersion`, and BOOKMARK events
- a deterministic, injectable clock

Scheduling is a filter-then-score scheduler. It honors required and preferred node affinity, inter-pod affinity/anti-affinity by `topologyKey`, and topology spread constraints, and scores nodes by the preferred (anti-)affinity weights and by minimizing topology-spread skew. By default there is one synthetic node. `cloudemu serve --k8s-nodes N` starts a multi-node cluster (a control-plane node with a `NoSchedule` taint, plus workers) with taints/tolerations and resource-request-vs-allocatable fit. Nodes can be added or removed at runtime. A Pod that can't be placed stays `Pending`/`Unschedulable`, and removing a node reschedules its Pods.

`exec`/`attach` run over a real WebSocket (v4/v5 channel protocols) as a deterministic synthetic session (a banner and Success; there is no container runtime). `kube-system` is seeded with coredns/kube-dns/kube-proxy. CronJobs fire on a wall-clock ticker when the opt-in progression ticker is running, or through `TickCronJobs` in tests.

Known simplifications:

- There is no real kubelet. Logs are synthetic and `pods/portforward` returns a typed 501.
- `NoExecute` taint-based eviction and `tolerationSeconds` only apply at scheduling time, not after a Pod is placed.
- Admission webhooks are only called when explicitly enabled.
- RBAC and NetworkPolicy can be queried but aren't enforced on requests.
- Rollouts converge instantly.

See [services.md §18](services.md#18-kubernetes) for the full resource list.

### Bedrock and Databricks

AWS Bedrock covers the `bedrock` control plane (foundation models, customization jobs, custom models, guardrails with policy configs and versions, provisioned throughput, invocation logging, resource tagging, model import/copy/evaluation jobs, inference profiles, prompt routers, marketplace model endpoints, foundation-model agreements, and automated-reasoning policies) and the `bedrock-runtime` data plane (InvokeModel with family-aware response envelopes, Converse, streaming ConverseStream / InvokeModelWithResponseStream over `vnd.amazon.eventstream`, CountTokens, ApplyGuardrail, and async invoke).

A separate AWS Bedrock Agent handler covers the `bedrock-agent` control plane (agents, knowledge bases, data sources, flows, prompts) and the `bedrock-agent-runtime` data plane (InvokeAgent streaming, Retrieve, RetrieveAndGenerate). Its runtime handler is registered before the control plane and only matches POST, so the two don't collide on the shared `/agents` and `/knowledgebases` roots. `bedrock-agent` covers only this core resource lifecycle and the runtime data plane. Agent versioning/aliases beyond basic create, action groups, and agent collaborators are not implemented yet.

Azure Databricks covers the `armdatabricks` ARM workspace resource and the `databricks-sdk-go` workspace data plane: clusters, instance pools, jobs and runs, cluster policies, libraries, permissions, secrets, tokens, git credentials, repos, DBFS, workspace notebooks/directories, SQL warehouses, pipelines, serving endpoints, SCIM identity, and Unity Catalog.

Bedrock caveats: long-running jobs (model customization, import and copy jobs; evaluation jobs start `InProgress`) complete synchronously, so Get/List see a terminal state right away instead of intermediate progress. Inference and agent responses (InvokeModel, Converse, InvokeAgent, RetrieveAndGenerate) are deterministic simulations, not real model output.

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

The `Handler` interface is the only contract; nothing needs to be registered in core CloudEmu. If the handler would be useful to others, a PR adding it under `server/<provider>/<service>` is welcome.

## Limitations

- No signature validation by default. CloudEmu is a local development tool, not a security boundary, and it accepts requests regardless of AWS SigV4 / Azure AAD / GCP OAuth signatures. The standalone server has an opt-in `--enforce-auth` flag (SigV4 verification for AWS, Bearer-token claim checks for Azure); see `cloudemu serve -h`.
- No AMQP for Azure Service Bus. The `azservicebus` SDK only uses AMQP for the data plane. The ARM control plane works through `armservicebus`, and tests that need send/receive can use the raw-HTTP REST data plane.
- GCS direct-media downloads assume path-style URLs.
- DynamoDB, Cosmos and Firestore queries use the real grammars: DynamoDB expressions (KeyCondition/Filter/Condition/Projection/Update), Firestore structured queries (composite filters, `orderBy`, cursors, `select`), and Cosmos SQL (`SELECT`/`WHERE`/`ORDER BY`/`OFFSET`-`LIMIT`/aggregates). Some advanced constructs are not supported (e.g. Cosmos `JOIN`/spatial/UDFs), and `NOT` over a composite predicate stays two-valued.
- Pagination tokens are honored where the SDK contract has them; some list operations always return a single page.
- Resource Graph `resourceGroup`. Rows get `resourceGroup` from the resource's ARM id. When a mock doesn't model a per-resource resource group, the id (and so `resourceGroup`) falls back to `default`, and all such resources share one resource group. That is fine for SKU/tier/size-based discovery and cost tests, but matters if your code keys on distinct resource groups. Event Hubs is modeled (`server/azure/eventhub`: `Microsoft.EventHub/namespaces` + `eventhubs` + `consumergroups`), so its namespaces do appear in Resource Graph.

For an unsupported operation, the server returns the provider's native error code, so the failure is easy to recognize.
