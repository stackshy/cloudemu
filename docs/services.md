# Provider Resource Reference

This document lists every service and operation available in CloudEmu across all three cloud providers.

## Master Table

| # | Service Category | AWS | Azure | GCP |
|---|-----------------|-----|-------|-----|
| 1 | Storage | `s3` | `blobstorage` | `gcs` |
| 2 | Compute | `ec2` | `virtualmachines` | `compute` |
| 3 | Database | `dynamodb` | `cosmosdb` | `firestore` |
| 4 | Serverless | `lambda` | `functions` | `cloudfunctions` |
| 5 | Networking | `vpc` (+ AWS-specific: Transit Gateway, VPN, DHCP options, prefix lists, egress-only IGW, endpoint services, Client VPN, Traffic Mirroring, Network Insights, VPC Block Public Access) | `vnet` | `vpc` |
| 5a | Network Firewall | `network-firewall` | — | — |
| 6 | Monitoring | `cloudwatch` | `monitor` | `monitoring` |
| 7 | IAM | `iam` | `iam` | `iam` |
| 8 | DNS | `route53` | `dns` | `clouddns` |
| 9 | Load Balancer | `elb` | `loadbalancer` | `loadbalancer` |
| 10 | Message Queue | `sqs` | `servicebus` | `pubsub` |
| 11 | Cache | `elasticache` | `cache` | `memorystore` |
| 12 | Secrets | `secretsmanager` | `keyvault` | `secretmanager` |
| 13 | Logging | `cloudwatchlogs` | `loganalytics` | `cloudlogging` |
| 14 | Notification | `sns` | `notificationhubs` | `fcm` |
| 15 | Container Registry | `ecr` | `acr` | `artifactregistry` |
| 16 | Event Bus | `eventbridge` | `eventgrid` | `eventarc` |
| 17 | Relational Database | `rds` (+ Aurora/Neptune/DocumentDB engines), `redshift` | `sql`, `postgresflex`, `mysqlflex` | `cloudsql`, `alloydb` |
| 17a | In-memory Database (Redis/Valkey) | `memorydb` | — | — |
| 17b | Wide-column (Cassandra) | `keyspaces` | `managedcassandra` | — |
| 17c | Wide-column (Bigtable) | — | — | `bigtable` |
| 17d | Distributed PostgreSQL (Citus) | — | `cosmospostgresql` | — |
| 18 | Kubernetes | `eks` + shared `services/kubernetes/` | `aks` + shared `services/kubernetes/` | `gke` + shared `services/kubernetes/` |
| 19 | Resource Discovery | `resourceexplorer2` + `resourcegroupstaggingapi` | `resourcegraph` | `cloudasset` |
| 20 | Generative AI | `bedrock` (+ `bedrock-runtime`), `bedrock-agent` (+ `bedrock-agent-runtime`) | — | — |
| 21 | Databricks | — | `databricks` | — |
| 22 | Machine Learning | `sagemaker` (+ `sagemaker-runtime`) | `ai` (CognitiveServices + MachineLearningServices) | `vertexai` |
| 23 | AI Search | — | `search` (Microsoft.Search) | — |
| 24 | Container Orchestration | `ecs` | — | — |
| 25 | DNS Resolver | `route53resolver` | — | — |
| 26 | Application Networking | `vpclattice` | — | — |
| 27 | Key Management | `kms` | — | — |
| 28 | File System | `efs` | — | — |
| 29 | Certificate Manager | `acm` | — | — |
| 30 | Email Service | `sesv2` | — | — |
| 31 | Web Application Firewall | `wafv2` | — | — |

---

## 1. Storage

**Driver interface:** `services/storage/driver/driver.go`
**AWS:** S3 | **Azure:** Blob Storage | **GCP:** GCS

### Bucket Operations

| Operation | Signature |
|-----------|-----------|
| `CreateBucket` | `(ctx, name) error` |
| `DeleteBucket` | `(ctx, name) error` |
| `ListBuckets` | `(ctx) ([]BucketInfo, error)` |

### Object Operations

| Operation | Signature |
|-----------|-----------|
| `PutObject` | `(ctx, bucket, key, data, contentType, metadata) error` |
| `GetObject` | `(ctx, bucket, key) (*Object, error)` |
| `DeleteObject` | `(ctx, bucket, key) error` |
| `HeadObject` | `(ctx, bucket, key) (*ObjectInfo, error)` |
| `ListObjects` | `(ctx, bucket, opts) (*ListResult, error)` |
| `CopyObject` | `(ctx, dstBucket, dstKey, src) error` |

### Presigned URLs

| Operation | Signature |
|-----------|-----------|
| `GeneratePresignedURL` | `(ctx, req) (*PresignedURL, error)` |

### Lifecycle Policies

| Operation | Signature |
|-----------|-----------|
| `PutLifecycleConfig` | `(ctx, bucket, config) error` |
| `GetLifecycleConfig` | `(ctx, bucket) (*LifecycleConfig, error)` |
| `EvaluateLifecycle` | `(ctx, bucket) ([]string, error)` |

### Multipart Uploads

| Operation | Signature |
|-----------|-----------|
| `CreateMultipartUpload` | `(ctx, bucket, key, contentType) (*MultipartUpload, error)` |
| `UploadPart` | `(ctx, bucket, key, uploadID, partNumber, data) (*UploadPart, error)` |
| `CompleteMultipartUpload` | `(ctx, bucket, key, uploadID, parts) error` |
| `AbortMultipartUpload` | `(ctx, bucket, key, uploadID) error` |
| `ListMultipartUploads` | `(ctx, bucket) ([]MultipartUpload, error)` |

### Versioning

| Operation | Signature |
|-----------|-----------|
| `SetBucketVersioning` | `(ctx, bucket, enabled) error` |
| `GetBucketVersioning` | `(ctx, bucket) (bool, error)` |

### Bucket Policy

| Operation | Signature |
|-----------|-----------|
| `PutBucketPolicy` | `(ctx, bucket, policy) error` |
| `GetBucketPolicy` | `(ctx, bucket) (*BucketPolicy, error)` |
| `DeleteBucketPolicy` | `(ctx, bucket) error` |

### CORS

| Operation | Signature |
|-----------|-----------|
| `PutCORSConfig` | `(ctx, bucket, config) error` |
| `GetCORSConfig` | `(ctx, bucket) (*CORSConfig, error)` |
| `DeleteCORSConfig` | `(ctx, bucket) error` |

### Encryption

| Operation | Signature |
|-----------|-----------|
| `PutEncryptionConfig` | `(ctx, bucket, config) error` |
| `GetEncryptionConfig` | `(ctx, bucket) (*EncryptionConfig, error)` |

### Object Tagging

| Operation | Signature |
|-----------|-----------|
| `PutObjectTagging` | `(ctx, bucket, key, tags) error` |
| `GetObjectTagging` | `(ctx, bucket, key) (map[string]string, error)` |
| `DeleteObjectTagging` | `(ctx, bucket, key) error` |

### Bucket Tagging

| Operation | Signature |
|-----------|-----------|
| `PutBucketTagging` | `(ctx, bucket, tags) error` |
| `GetBucketTagging` | `(ctx, bucket) (map[string]string, error)` |
| `DeleteBucketTagging` | `(ctx, bucket) error` |

**Total: 33 operations**

---

## 2. Compute

**Driver interface:** `services/compute/driver/driver.go`
**AWS:** EC2 | **Azure:** Virtual Machines | **GCP:** GCE

### Instance Operations

| Operation | Signature |
|-----------|-----------|
| `RunInstances` | `(ctx, config, count) ([]Instance, error)` |
| `StartInstances` | `(ctx, instanceIDs) error` |
| `StopInstances` | `(ctx, instanceIDs) error` |
| `RebootInstances` | `(ctx, instanceIDs) error` |
| `TerminateInstances` | `(ctx, instanceIDs) error` |
| `DescribeInstances` | `(ctx, instanceIDs, filters, ...opts) ([]Instance, error)` |
| `ModifyInstance` | `(ctx, instanceID, input) error` |

#### Managed-resource visibility

EC2 emulates AWS *managed resources* — instances an AWS service (e.g. ECS Managed
Instances, EKS Auto Mode) provisions on the account's behalf. A managed instance
carries an `Operator` block (`Managed=true`, `Principal`) and is **hidden from
`DescribeInstances` by default** once the account's visibility is set to `hidden`,
reappearing only when the caller opts in with `IncludeManagedResources=true`.
Non-managed instances are always returned.

```go
cloud := cloudemu.NewAWS()
cloud.EC2.RunInstances(ctx, computedriver.InstanceConfig{
    InstanceType: "m5.large",
    Managed:      true,                  // Operator.Managed = true
    Principal:    "ecs.amazonaws.com",   // Operator.Principal
    Tags:         map[string]string{"aws:ec2:managed-launch": "ecs-managed-instances"},
}, 1)
cloud.EC2.SetManagedResourceVisibility("hidden")

// Go API
cloud.EC2.DescribeInstances(ctx, nil, nil)                                            // managed instance omitted
cloud.EC2.DescribeInstances(ctx, nil, nil, computedriver.DescribeInstancesOptions{IncludeManagedResources: true}) // included

// SDK-compat: real aws-sdk-go-v2 ec2.Client
client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})                                // omits managed
client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{IncludeManagedResources: aws.Bool(true)}) // includes managed
```

### Auto-Scaling Groups (ASG)

| Operation | Signature |
|-----------|-----------|
| `CreateAutoScalingGroup` | `(ctx, config) (*AutoScalingGroup, error)` |
| `DeleteAutoScalingGroup` | `(ctx, name, forceDelete) error` |
| `GetAutoScalingGroup` | `(ctx, name) (*AutoScalingGroup, error)` |
| `ListAutoScalingGroups` | `(ctx) ([]AutoScalingGroup, error)` |
| `UpdateAutoScalingGroup` | `(ctx, name, desired, minSize, maxSize) error` |
| `SetDesiredCapacity` | `(ctx, name, desired) error` |

### Scaling Policies

| Operation | Signature |
|-----------|-----------|
| `PutScalingPolicy` | `(ctx, policy) error` |
| `DeleteScalingPolicy` | `(ctx, asgName, policyName) error` |
| `ExecuteScalingPolicy` | `(ctx, asgName, policyName) error` |

### Spot/Preemptible Instances

| Operation | Signature |
|-----------|-----------|
| `RequestSpotInstances` | `(ctx, config) ([]SpotInstanceRequest, error)` |
| `CancelSpotRequests` | `(ctx, requestIDs) error` |
| `DescribeSpotRequests` | `(ctx, requestIDs) ([]SpotInstanceRequest, error)` |

### Launch Templates

| Operation | Signature |
|-----------|-----------|
| `CreateLaunchTemplate` | `(ctx, config) (*LaunchTemplate, error)` |
| `DeleteLaunchTemplate` | `(ctx, name) error` |
| `GetLaunchTemplate` | `(ctx, name) (*LaunchTemplate, error)` |
| `ListLaunchTemplates` | `(ctx) ([]LaunchTemplate, error)` |

### Volumes

| Operation | Signature |
|-----------|-----------|
| `CreateVolume` | `(ctx, config) (*VolumeInfo, error)` |
| `DeleteVolume` | `(ctx, id) error` |
| `DescribeVolumes` | `(ctx, ids) ([]VolumeInfo, error)` |
| `AttachVolume` | `(ctx, volumeID, instanceID, device) error` |
| `DetachVolume` | `(ctx, volumeID) error` |

### Snapshots

| Operation | Signature |
|-----------|-----------|
| `CreateSnapshot` | `(ctx, config) (*SnapshotInfo, error)` |
| `DeleteSnapshot` | `(ctx, id) error` |
| `DescribeSnapshots` | `(ctx, ids) ([]SnapshotInfo, error)` |

### Images

| Operation | Signature |
|-----------|-----------|
| `CreateImage` | `(ctx, config) (*ImageInfo, error)` |
| `DeregisterImage` | `(ctx, id) error` |
| `DescribeImages` | `(ctx, ids) ([]ImageInfo, error)` |

### Key Pairs

| Operation | Signature |
|-----------|-----------|
| `CreateKeyPair` | `(ctx, config) (*KeyPairInfo, error)` |
| `DeleteKeyPair` | `(ctx, name) error` |
| `DescribeKeyPairs` | `(ctx, names) ([]KeyPairInfo, error)` |

**Total: 35 operations**

---

## 3. Database

**Driver interface:** `services/database/driver/driver.go`
**AWS:** DynamoDB | **Azure:** Cosmos DB | **GCP:** Firestore

### Table Operations

| Operation | Signature |
|-----------|-----------|
| `CreateTable` | `(ctx, config) error` |
| `DeleteTable` | `(ctx, name) error` |
| `DescribeTable` | `(ctx, name) (*TableConfig, error)` |
| `ListTables` | `(ctx) ([]string, error)` |

### Item Operations

| Operation | Signature |
|-----------|-----------|
| `PutItem` | `(ctx, table, item) error` |
| `GetItem` | `(ctx, table, key) (map[string]any, error)` |
| `UpdateItem` | `(ctx, input) (map[string]any, error)` |
| `DeleteItem` | `(ctx, table, key) error` |
| `Query` | `(ctx, input) (*QueryResult, error)` |
| `Scan` | `(ctx, input) (*QueryResult, error)` |

### Batch Operations

| Operation | Signature |
|-----------|-----------|
| `BatchPutItems` | `(ctx, table, items) error` |
| `BatchGetItems` | `(ctx, table, keys) ([]map[string]any, error)` |

### TTL

| Operation | Signature |
|-----------|-----------|
| `UpdateTTL` | `(ctx, table, config) error` |
| `DescribeTTL` | `(ctx, table) (*TTLConfig, error)` |

### Streams / Change Feed

| Operation | Signature |
|-----------|-----------|
| `UpdateStreamConfig` | `(ctx, table, config) error` |
| `GetStreamRecords` | `(ctx, table, limit, token) (*StreamIterator, error)` |

### Transactions

| Operation | Signature |
|-----------|-----------|
| `TransactWriteItems` | `(ctx, table, puts, deletes) error` |

### Global Secondary Indexes (GSI)

| Operation | Signature |
|-----------|-----------|
| `CreateIndex` | `(ctx, table, config) (*IndexInfo, error)` |
| `DeleteIndex` | `(ctx, table, indexName) error` |
| `DescribeIndex` | `(ctx, table, indexName) (*IndexInfo, error)` |
| `ListIndexes` | `(ctx, table) ([]IndexInfo, error)` |

**Total: 21 operations**

---

## 4. Serverless

**Driver interface:** `services/serverless/driver/driver.go`
**AWS:** Lambda | **Azure:** Functions | **GCP:** Cloud Functions

### Function Operations

| Operation | Signature |
|-----------|-----------|
| `CreateFunction` | `(ctx, config) (*FunctionInfo, error)` |
| `DeleteFunction` | `(ctx, name) error` |
| `GetFunction` | `(ctx, name) (*FunctionInfo, error)` |
| `ListFunctions` | `(ctx) ([]FunctionInfo, error)` |
| `UpdateFunction` | `(ctx, name, config) (*FunctionInfo, error)` |
| `Invoke` | `(ctx, input) (*InvokeOutput, error)` |
| `RegisterHandler` | `(name, handler)` |

### Versions

| Operation | Signature |
|-----------|-----------|
| `PublishVersion` | `(ctx, functionName, description) (*FunctionVersion, error)` |
| `ListVersions` | `(ctx, functionName) ([]FunctionVersion, error)` |

### Aliases

| Operation | Signature |
|-----------|-----------|
| `CreateAlias` | `(ctx, config) (*Alias, error)` |
| `UpdateAlias` | `(ctx, config) (*Alias, error)` |
| `DeleteAlias` | `(ctx, functionName, aliasName) error` |
| `GetAlias` | `(ctx, functionName, aliasName) (*Alias, error)` |
| `ListAliases` | `(ctx, functionName) ([]Alias, error)` |

### Layers

| Operation | Signature |
|-----------|-----------|
| `PublishLayerVersion` | `(ctx, config) (*LayerVersion, error)` |
| `GetLayerVersion` | `(ctx, name, version) (*LayerVersion, error)` |
| `ListLayerVersions` | `(ctx, name) ([]LayerVersion, error)` |
| `DeleteLayerVersion` | `(ctx, name, version) error` |
| `ListLayers` | `(ctx) ([]LayerVersion, error)` |

### Concurrency

| Operation | Signature |
|-----------|-----------|
| `PutFunctionConcurrency` | `(ctx, config) error` |
| `GetFunctionConcurrency` | `(ctx, functionName) (*ConcurrencyConfig, error)` |
| `DeleteFunctionConcurrency` | `(ctx, functionName) error` |

### Event Source Mappings

| Operation | Signature |
|-----------|-----------|
| `CreateEventSourceMapping` | `(ctx, config) (*EventSourceMappingInfo, error)` |
| `DeleteEventSourceMapping` | `(ctx, uuid) error` |
| `GetEventSourceMapping` | `(ctx, uuid) (*EventSourceMappingInfo, error)` |
| `ListEventSourceMappings` | `(ctx, functionName) ([]EventSourceMappingInfo, error)` |
| `UpdateEventSourceMapping` | `(ctx, uuid, config) (*EventSourceMappingInfo, error)` |

**Total: 26 operations**

---

## 5. Networking

**Driver interface:** `services/networking/driver/driver.go`
**AWS:** VPC | **Azure:** VNet | **GCP:** GCP VPC

### VPC Operations

| Operation | Signature |
|-----------|-----------|
| `CreateVPC` | `(ctx, config) (*VPCInfo, error)` |
| `DeleteVPC` | `(ctx, id) error` |
| `DescribeVPCs` | `(ctx, ids) ([]VPCInfo, error)` |

### Subnets

| Operation | Signature |
|-----------|-----------|
| `CreateSubnet` | `(ctx, config) (*SubnetInfo, error)` |
| `DeleteSubnet` | `(ctx, id) error` |
| `DescribeSubnets` | `(ctx, ids) ([]SubnetInfo, error)` |

### Security Groups

| Operation | Signature |
|-----------|-----------|
| `CreateSecurityGroup` | `(ctx, config) (*SecurityGroupInfo, error)` |
| `DeleteSecurityGroup` | `(ctx, id) error` |
| `DescribeSecurityGroups` | `(ctx, ids) ([]SecurityGroupInfo, error)` |
| `AddIngressRule` | `(ctx, groupID, rule) error` |
| `AddEgressRule` | `(ctx, groupID, rule) error` |
| `RemoveIngressRule` | `(ctx, groupID, rule) error` |
| `RemoveEgressRule` | `(ctx, groupID, rule) error` |

### VPC Peering

| Operation | Signature |
|-----------|-----------|
| `CreatePeeringConnection` | `(ctx, config) (*PeeringConnection, error)` |
| `AcceptPeeringConnection` | `(ctx, peeringID) error` |
| `RejectPeeringConnection` | `(ctx, peeringID) error` |
| `DeletePeeringConnection` | `(ctx, peeringID) error` |
| `DescribePeeringConnections` | `(ctx, ids) ([]PeeringConnection, error)` |

### NAT Gateways

| Operation | Signature |
|-----------|-----------|
| `CreateNATGateway` | `(ctx, config) (*NATGateway, error)` |
| `DeleteNATGateway` | `(ctx, id) error` |
| `DescribeNATGateways` | `(ctx, ids) ([]NATGateway, error)` |

### Flow Logs

| Operation | Signature |
|-----------|-----------|
| `CreateFlowLog` | `(ctx, config) (*FlowLog, error)` |
| `DeleteFlowLog` | `(ctx, id) error` |
| `DescribeFlowLogs` | `(ctx, ids) ([]FlowLog, error)` |
| `GetFlowLogRecords` | `(ctx, flowLogID, limit) ([]FlowLogRecord, error)` |

### Route Tables

| Operation | Signature |
|-----------|-----------|
| `CreateRouteTable` | `(ctx, config) (*RouteTable, error)` |
| `DeleteRouteTable` | `(ctx, id) error` |
| `DescribeRouteTables` | `(ctx, ids) ([]RouteTable, error)` |
| `CreateRoute` | `(ctx, routeTableID, destinationCIDR, targetID, targetType) error` |
| `DeleteRoute` | `(ctx, routeTableID, destinationCIDR) error` |

### Network ACLs

| Operation | Signature |
|-----------|-----------|
| `CreateNetworkACL` | `(ctx, vpcID, tags) (*NetworkACL, error)` |
| `DeleteNetworkACL` | `(ctx, id) error` |
| `DescribeNetworkACLs` | `(ctx, ids) ([]NetworkACL, error)` |
| `AddNetworkACLRule` | `(ctx, aclID, rule) error` |
| `RemoveNetworkACLRule` | `(ctx, aclID, ruleNumber, egress) error` |

### Internet Gateways (IGW)

| Operation | Signature |
|-----------|-----------|
| `CreateInternetGateway` | `(ctx, config) (*InternetGateway, error)` |
| `DeleteInternetGateway` | `(ctx, id) error` |
| `DescribeInternetGateways` | `(ctx, ids) ([]InternetGateway, error)` |
| `AttachInternetGateway` | `(ctx, igwID, vpcID) error` |
| `DetachInternetGateway` | `(ctx, igwID, vpcID) error` |

### Elastic IPs (EIP)

| Operation | Signature |
|-----------|-----------|
| `AllocateAddress` | `(ctx, config) (*ElasticIP, error)` |
| `ReleaseAddress` | `(ctx, allocationID) error` |
| `DescribeAddresses` | `(ctx, ids) ([]ElasticIP, error)` |
| `AssociateAddress` | `(ctx, allocationID, instanceID) (string, error)` |
| `DisassociateAddress` | `(ctx, associationID) error` |

### Route Table Associations

| Operation | Signature |
|-----------|-----------|
| `AssociateRouteTable` | `(ctx, routeTableID, subnetID) (*RouteTableAssociation, error)` |
| `DisassociateRouteTable` | `(ctx, associationID) error` |

Every VPC is created with a main route table, carrying the local route and an
association with `Main: true` and no subnet. It cannot be deleted or
disassociated on its own and disappears with the VPC. A subnet with no explicit
association is governed by it.

`DescribeRouteTables` populates `RouteTable.Associations`; it is the only way a
caller can discover an association ID in order to disassociate.

### Network Interfaces (ENI)

| Operation | Signature |
|-----------|-----------|
| `DescribeNetworkInterfaces` | `(ctx, ids) ([]NetworkInterface, error)` |
| `DetachNetworkInterface` | `(ctx, attachmentID, force) error` |
| `DeleteNetworkInterface` | `(ctx, id) error` |

Managed resources attach interfaces of their own — a NAT gateway holds one for
as long as it lives. An attached interface cannot be deleted, which is how a
caller draining a VPC before deleting it learns the drain is not finished.

### VPC Attributes

| Operation | Signature |
|-----------|-----------|
| `ModifyVPCAttribute` | `(ctx, id, enableDNSSupport, enableDNSHostnames) error` |

Both attributes are pointers: `nil` leaves that attribute unchanged, matching an
API that accepts one attribute per call. New VPCs default to DNS support on and
DNS hostnames off.

### VPC Endpoints

| Operation | Signature |
|-----------|-----------|
| `CreateVPCEndpoint` | `(ctx, config) (*VPCEndpoint, error)` |
| `DeleteVPCEndpoint` | `(ctx, id) error` |
| `DescribeVPCEndpoints` | `(ctx, ids) ([]VPCEndpoint, error)` |
| `ModifyVPCEndpoint` | `(ctx, id, config) (*VPCEndpoint, error)` |

**Total: 47 operations**

### AWS-specific networking (optional capabilities)

AWS models several networking resources that don't map cleanly across clouds.
These are **AWS-only optional capability interfaces** (discovered by type
assertion, like `NetworkInterfaces`/`VPCAttributes`) implemented by
`providers/aws/vpc` and served by the EC2 handler — no Azure/GCP stubs.

| Capability | Operations |
|-----------|-----------|
| Transit Gateway | CreateTransitGateway, DeleteTransitGateway, DescribeTransitGateways; VPC attachments (Create/Delete/Describe); route tables (Create/Delete/Describe); routes (Create/Delete/Search); route-table Associate + Enable/DisableRouteTablePropagation |
| VPN | CustomerGateway (Create/Delete/Describe); VpnGateway (Create/Delete/Describe/Attach/Detach); VpnConnection (Create/Delete/Describe/ModifyVpnConnection); VpnConnectionRoute (Create/Delete) |
| DHCP option sets | Create, Delete, Describe, Associate |
| Managed prefix lists | Create, Delete, Describe, GetEntries, Modify |
| Egress-only internet gateways | Create, Delete, Describe |
| VPC endpoint services (PrivateLink) | Create, Delete, Describe; ModifyPermissions, DescribePermissions |
| Client VPN | CreateEndpoint, DeleteEndpoint, DescribeEndpoints, Associate/DisassociateTargetNetwork, DescribeTargetNetworks; Authorize/RevokeIngress, DescribeAuthorizationRules; Route (Create/Delete/Describe) |
| Traffic Mirroring | Target (Create/Delete/Describe); Filter (Create/Delete/Describe) + ModifyFilterNetworkServices; FilterRule (Create/Modify/Delete/Describe); Session (Create/Modify/Delete/Describe) |
| Network Insights — Reachability Analyzer | Path (Create/Delete/Describe); Analysis (Start/Delete/Describe) |
| Network Insights — Network Access Analyzer | AccessScope (Create/Delete/Describe) + GetContent; AccessScopeAnalysis (Start/Delete/Describe) + GetAnalysisFindings |
| VPC Block Public Access | Options (Describe/Modify); Exclusion (Create/Modify/Delete/Describe) |
| IPAM (IP Address Manager) — full | Ipam/Scope/Pool CRUD+Modify; Cidr Provision/Deprovision/Get; Allocation Allocate/Release/Get/Modify; ResourceCidrs (Get/Modify) + AddressHistory; ResourceDiscovery CRUD + Associate/Disassociate + Discovered Accounts/ResourceCidrs/PublicAddresses; BYOASN (Provision/Deprovision/Associate/Disassociate/Describe); BYOIP (Move/Provision/Deprovision/Describe/Advertise/Withdraw); PrefixListResolver + Targets + Versions/Rules/Entries; ExternalResourceVerificationToken (Create/Delete/Describe); Policy (Create/Delete/Describe/Enable/Disable/GetEnabled/AllocationRules/OrgTargets) + OrganizationAdminAccount (Enable/Disable) |

**AWS-specific total: 162 operations**

IPAM is fully covered (~69 operations). Cross-account/organization and live-network features (Resource Discovery, discovered accounts/resources/public addresses, BYOASN/BYOIP, policies, org-admin) are modeled against the emulator's own single-account state: discovered resources are derived from the stored VPCs/subnets/EIPs, and organization targets resolve to the configured account.

### IPAM metrics (`AWS/IPAM` CloudWatch namespace)

IPAM publishes derived metrics through the CloudWatch service (ListMetrics / GetMetricStatistics): `TotalActiveIpCount`; pool `PercentAllocated`/`PercentAssigned`/`PercentAvailable`/`Compliant`/`NoncompliantResourceCidrs`; scope `Managed`/`Unmanaged`/`Overlapping`/`Compliant`/`NoncompliantResourceCidrs`; public-IP insight counts; and resource utilization `VpcIPUsage`/`SubnetIPUsage`. Values are computed live from IPAM + VPC/subnet/EIP state.

---

## 6. Monitoring

**Driver interface:** `services/monitoring/driver/driver.go`
**AWS:** CloudWatch | **Azure:** Azure Monitor | **GCP:** Cloud Monitoring

### Metric Operations

| Operation | Signature |
|-----------|-----------|
| `PutMetricData` | `(ctx, data) error` |
| `GetMetricData` | `(ctx, input) (*MetricDataResult, error)` |
| `ListMetrics` | `(ctx, namespace) ([]string, error)` |

### Alarm Operations

| Operation | Signature |
|-----------|-----------|
| `CreateAlarm` | `(ctx, config) error` |
| `DeleteAlarm` | `(ctx, name) error` |
| `DescribeAlarms` | `(ctx, names) ([]AlarmInfo, error)` |
| `SetAlarmState` | `(ctx, name, state, reason) error` |

### Notification Channels

| Operation | Signature |
|-----------|-----------|
| `CreateNotificationChannel` | `(ctx, config) (*NotificationChannelInfo, error)` |
| `DeleteNotificationChannel` | `(ctx, id) error` |
| `GetNotificationChannel` | `(ctx, id) (*NotificationChannelInfo, error)` |
| `ListNotificationChannels` | `(ctx) ([]NotificationChannelInfo, error)` |

### Alarm History

| Operation | Signature |
|-----------|-----------|
| `GetAlarmHistory` | `(ctx, alarmName, limit) ([]AlarmHistoryEntry, error)` |

**Total: 12 operations**

---

## 7. IAM

**Driver interface:** `services/iam/driver/driver.go`
**AWS:** IAM | **Azure:** Azure IAM | **GCP:** GCP IAM

### Users

| Operation | Signature |
|-----------|-----------|
| `CreateUser` | `(ctx, config) (*UserInfo, error)` |
| `DeleteUser` | `(ctx, name) error` |
| `GetUser` | `(ctx, name) (*UserInfo, error)` |
| `ListUsers` | `(ctx) ([]UserInfo, error)` |

### Roles

| Operation | Signature |
|-----------|-----------|
| `CreateRole` | `(ctx, config) (*RoleInfo, error)` |
| `DeleteRole` | `(ctx, name) error` |
| `GetRole` | `(ctx, name) (*RoleInfo, error)` |
| `ListRoles` | `(ctx) ([]RoleInfo, error)` |

### Policies

| Operation | Signature |
|-----------|-----------|
| `CreatePolicy` | `(ctx, config) (*PolicyInfo, error)` |
| `DeletePolicy` | `(ctx, arn) error` |
| `GetPolicy` | `(ctx, arn) (*PolicyInfo, error)` |
| `ListPolicies` | `(ctx) ([]PolicyInfo, error)` |

### Policy Attachments

| Operation | Signature |
|-----------|-----------|
| `AttachUserPolicy` | `(ctx, userName, policyARN) error` |
| `DetachUserPolicy` | `(ctx, userName, policyARN) error` |
| `AttachRolePolicy` | `(ctx, roleName, policyARN) error` |
| `DetachRolePolicy` | `(ctx, roleName, policyARN) error` |
| `ListAttachedUserPolicies` | `(ctx, userName) ([]string, error)` |
| `ListAttachedRolePolicies` | `(ctx, roleName) ([]string, error)` |

### Permission Evaluation

| Operation | Signature |
|-----------|-----------|
| `CheckPermission` | `(ctx, principal, action, resource) (bool, error)` |

### Groups

| Operation | Signature |
|-----------|-----------|
| `CreateGroup` | `(ctx, config) (*GroupInfo, error)` |
| `DeleteGroup` | `(ctx, name) error` |
| `GetGroup` | `(ctx, name) (*GroupInfo, error)` |
| `ListGroups` | `(ctx) ([]GroupInfo, error)` |
| `AddUserToGroup` | `(ctx, userName, groupName) error` |
| `RemoveUserFromGroup` | `(ctx, userName, groupName) error` |
| `ListGroupsForUser` | `(ctx, userName) ([]GroupInfo, error)` |

### Access Keys

| Operation | Signature |
|-----------|-----------|
| `CreateAccessKey` | `(ctx, config) (*AccessKeyInfo, error)` |
| `DeleteAccessKey` | `(ctx, userName, accessKeyID) error` |
| `ListAccessKeys` | `(ctx, userName) ([]AccessKeyInfo, error)` |

### Instance Profiles

| Operation | Signature |
|-----------|-----------|
| `CreateInstanceProfile` | `(ctx, config) (*InstanceProfileInfo, error)` |
| `DeleteInstanceProfile` | `(ctx, name) error` |
| `GetInstanceProfile` | `(ctx, name) (*InstanceProfileInfo, error)` |
| `ListInstanceProfiles` | `(ctx) ([]InstanceProfileInfo, error)` |
| `AddRoleToInstanceProfile` | `(ctx, profileName, roleName) error` |
| `RemoveRoleFromInstanceProfile` | `(ctx, profileName, roleName) error` |

**Total: 35 operations**

---

## 8. DNS

**Driver interface:** `services/dns/driver/driver.go`
**AWS:** Route 53 | **Azure:** Azure DNS | **GCP:** Cloud DNS

### Zone Operations

| Operation | Signature |
|-----------|-----------|
| `CreateZone` | `(ctx, config) (*ZoneInfo, error)` |
| `DeleteZone` | `(ctx, id) error` |
| `GetZone` | `(ctx, id) (*ZoneInfo, error)` |
| `ListZones` | `(ctx) ([]ZoneInfo, error)` |

### Record Operations

| Operation | Signature |
|-----------|-----------|
| `CreateRecord` | `(ctx, config) (*RecordInfo, error)` |
| `DeleteRecord` | `(ctx, zoneID, name, recordType) error` |
| `GetRecord` | `(ctx, zoneID, name, recordType) (*RecordInfo, error)` |
| `ListRecords` | `(ctx, zoneID) ([]RecordInfo, error)` |
| `UpdateRecord` | `(ctx, config) (*RecordInfo, error)` |

### Health Checks

| Operation | Signature |
|-----------|-----------|
| `CreateHealthCheck` | `(ctx, config) (*HealthCheckInfo, error)` |
| `DeleteHealthCheck` | `(ctx, id) error` |
| `GetHealthCheck` | `(ctx, id) (*HealthCheckInfo, error)` |
| `ListHealthChecks` | `(ctx) ([]HealthCheckInfo, error)` |
| `UpdateHealthCheck` | `(ctx, id, config) (*HealthCheckInfo, error)` |
| `SetHealthCheckStatus` | `(ctx, id, status) error` |

**Total: 15 operations**

---

## 9. Load Balancer

**Driver interface:** `services/loadbalancer/driver/driver.go`
**AWS:** ELB | **Azure:** Azure LB | **GCP:** GCP LB

### Load Balancer Operations

| Operation | Signature |
|-----------|-----------|
| `CreateLoadBalancer` | `(ctx, config) (*LBInfo, error)` |
| `DeleteLoadBalancer` | `(ctx, arn) error` |
| `DescribeLoadBalancers` | `(ctx, arns) ([]LBInfo, error)` |

### Target Groups

| Operation | Signature |
|-----------|-----------|
| `CreateTargetGroup` | `(ctx, config) (*TargetGroupInfo, error)` |
| `DeleteTargetGroup` | `(ctx, arn) error` |
| `DescribeTargetGroups` | `(ctx, arns) ([]TargetGroupInfo, error)` |

### Listeners

| Operation | Signature |
|-----------|-----------|
| `CreateListener` | `(ctx, config) (*ListenerInfo, error)` |
| `DeleteListener` | `(ctx, arn) error` |
| `DescribeListeners` | `(ctx, lbARN) ([]ListenerInfo, error)` |
| `ModifyListener` | `(ctx, input) error` |

### Rules

| Operation | Signature |
|-----------|-----------|
| `CreateRule` | `(ctx, config) (*RuleInfo, error)` |
| `DeleteRule` | `(ctx, ruleARN) error` |
| `DescribeRules` | `(ctx, listenerARN) ([]RuleInfo, error)` |

### Attributes

| Operation | Signature |
|-----------|-----------|
| `GetLBAttributes` | `(ctx, lbARN) (*LBAttributes, error)` |
| `PutLBAttributes` | `(ctx, lbARN, attrs) error` |

These two were always in the driver; they are listed here because the ELBv2
handler now exposes them as ModifyLoadBalancerAttributes and
DescribeLoadBalancerAttributes.

### Targets

| Operation | Signature |
|-----------|-----------|
| `RegisterTargets` | `(ctx, targetGroupARN, targets) error` |
| `DeregisterTargets` | `(ctx, targetGroupARN, targets) error` |
| `DescribeTargetHealth` | `(ctx, targetGroupARN) ([]TargetHealth, error)` |
| `SetTargetHealth` | `(ctx, targetGroupARN, targetID, state) error` |

### Attributes

| Operation | Signature |
|-----------|-----------|
| `GetLBAttributes` | `(ctx, lbARN) (*LBAttributes, error)` |
| `PutLBAttributes` | `(ctx, lbARN, attrs) error` |

These two were always in the driver; they are listed here because the ELBv2
handler now exposes them as ModifyLoadBalancerAttributes and
DescribeLoadBalancerAttributes.

`LBAttributes.Extra` carries attributes outside the typed set, keyed by their
provider attribute name (`load_balancing.cross_zone.enabled` and friends).
Providers model attributes as open key/value pairs and add new ones over time, so
a fixed struct would silently drop whatever it had not been taught.

**Total: 21 operations**

---

## 10. Message Queue

**Driver interface:** `services/messagequeue/driver/driver.go`
**AWS:** SQS | **Azure:** Service Bus | **GCP:** Pub/Sub

### Queue Operations

| Operation | Signature |
|-----------|-----------|
| `CreateQueue` | `(ctx, config) (*QueueInfo, error)` |
| `DeleteQueue` | `(ctx, url) error` |
| `GetQueueInfo` | `(ctx, url) (*QueueInfo, error)` |
| `ListQueues` | `(ctx, prefix) ([]QueueInfo, error)` |

### Message Operations

| Operation | Signature |
|-----------|-----------|
| `SendMessage` | `(ctx, input) (*SendMessageOutput, error)` |
| `ReceiveMessages` | `(ctx, input) ([]Message, error)` |
| `DeleteMessage` | `(ctx, queueURL, receiptHandle) error` |
| `ChangeVisibility` | `(ctx, queueURL, receiptHandle, timeout) error` |

### Batch Operations

| Operation | Signature |
|-----------|-----------|
| `SendMessageBatch` | `(ctx, queue, entries) (*BatchSendResult, error)` |
| `DeleteMessageBatch` | `(ctx, queue, entries) (*BatchDeleteResult, error)` |

### Enhanced Receive

| Operation | Signature |
|-----------|-----------|
| `ReceiveMessagesWithOptions` | `(ctx, queue, opts) ([]Message, error)` |

### Queue Attributes

| Operation | Signature |
|-----------|-----------|
| `GetQueueAttributes` | `(ctx, queue) (*QueueAttributes, error)` |
| `SetQueueAttributes` | `(ctx, queue, attrs) error` |

### Purge

| Operation | Signature |
|-----------|-----------|
| `PurgeQueue` | `(ctx, queue) error` |

**Total: 14 operations**

---

## 11. Cache

**Driver interface:** `services/cache/driver/driver.go`
**AWS:** ElastiCache | **Azure:** Azure Cache | **GCP:** Memorystore

### Cache Instance Operations

| Operation | Signature |
|-----------|-----------|
| `CreateCache` | `(ctx, config) (*CacheInfo, error)` |
| `DeleteCache` | `(ctx, name) error` |
| `GetCache` | `(ctx, name) (*CacheInfo, error)` |
| `ListCaches` | `(ctx) ([]CacheInfo, error)` |

### Data Operations

| Operation | Signature |
|-----------|-----------|
| `Set` | `(ctx, cacheName, key, value, ttl) error` |
| `Get` | `(ctx, cacheName, key) (*Item, error)` |
| `Delete` | `(ctx, cacheName, key) error` |
| `Keys` | `(ctx, cacheName, pattern) ([]string, error)` |
| `FlushAll` | `(ctx, cacheName) error` |

### TTL Management

| Operation | Signature |
|-----------|-----------|
| `Expire` | `(ctx, cacheName, key, ttl) error` |
| `GetTTL` | `(ctx, cacheName, key) (time.Duration, error)` |
| `Persist` | `(ctx, cacheName, key) error` |

### Atomic Counters

| Operation | Signature |
|-----------|-----------|
| `Incr` | `(ctx, cacheName, key) (int64, error)` |
| `IncrBy` | `(ctx, cacheName, key, delta) (int64, error)` |
| `Decr` | `(ctx, cacheName, key) (int64, error)` |
| `DecrBy` | `(ctx, cacheName, key, delta) (int64, error)` |

### Subnet Groups (optional capability)

| Operation | Signature |
|-----------|-----------|
| `CreateCacheSubnetGroup` | `(ctx, SubnetGroupConfig) (*SubnetGroup, error)` |
| `DescribeCacheSubnetGroups` | `(ctx, names) ([]SubnetGroup, error)` |
| `DeleteCacheSubnetGroup` | `(ctx, name) error` |

### Replication Groups (optional capability)

A primary node plus replicas, addressed through one primary endpoint. Callers
build a connection string from it, so the endpoint is always populated — a group
without one is indistinguishable from a broken provision.

| Operation | Signature |
|-----------|-----------|
| `CreateReplicationGroup` | `(ctx, ReplicationGroupConfig) (*ReplicationGroup, error)` |
| `DescribeReplicationGroups` | `(ctx, ids) ([]ReplicationGroup, error)` |
| `ModifyReplicationGroup` | `(ctx, id, numCacheNodes) (*ReplicationGroup, error)` |
| `DeleteReplicationGroup` | `(ctx, id) error` |

Both interfaces are AWS-only concepts, discovered by type assertion.

**Total: 16 operations (+7 optional)**

---

## 11a. MemoryDB (AWS)

**Driver interface:** `services/memorydb/driver/driver.go`
**AWS:** MemoryDB for Redis/Valkey | **Azure:** — | **GCP:** —

A durable, in-VPC Redis/Valkey cluster service. Unlike Cache, MemoryDB is a
control-plane-only surface (no `Set`/`Get` data plane), so it has its own driver
rather than reusing `services/cache`. Served as AWS JSON 1.1 on the
`AmazonMemoryDB.` target prefix (`server/aws/memorydb`), so a real
`aws-sdk-go-v2/service/memorydb` client with a custom endpoint works unchanged.

### Clusters (shards, nodes, endpoints)

| Operation | Signature |
|-----------|-----------|
| `CreateCluster` | `(ctx, CreateClusterConfig) (*Cluster, error)` |
| `DescribeClusters` | `(ctx, names) ([]Cluster, error)` |
| `UpdateCluster` | `(ctx, UpdateClusterConfig) (*Cluster, error)` |
| `DeleteCluster` | `(ctx, name, finalSnapshotName) (*Cluster, error)` |
| `FailoverShard` | `(ctx, clusterName, shardName) (*Cluster, error)` |
| `ListAllowedNodeTypeUpdates` | `(ctx, clusterName) (scaleUp, scaleDown []string, error)` |

### ACLs & Users

| Operation | Signature |
|-----------|-----------|
| `CreateACL` | `(ctx, name, userNames, tags) (*ACL, error)` |
| `DescribeACLs` | `(ctx, names) ([]ACL, error)` |
| `UpdateACL` | `(ctx, name, add, remove) (*ACL, error)` |
| `DeleteACL` | `(ctx, name) (*ACL, error)` |
| `CreateUser` | `(ctx, CreateUserConfig) (*User, error)` |
| `DescribeUsers` | `(ctx, names) ([]User, error)` |
| `UpdateUser` | `(ctx, UpdateUserConfig) (*User, error)` |
| `DeleteUser` | `(ctx, name) (*User, error)` |

### Parameter Groups

| Operation | Signature |
|-----------|-----------|
| `CreateParameterGroup` | `(ctx, name, family, description, tags) (*ParameterGroup, error)` |
| `DescribeParameterGroups` | `(ctx, names) ([]ParameterGroup, error)` |
| `UpdateParameterGroup` | `(ctx, name, params) (*ParameterGroup, error)` |
| `ResetParameterGroup` | `(ctx, name, all, names) (*ParameterGroup, error)` |
| `DeleteParameterGroup` | `(ctx, name) (*ParameterGroup, error)` |
| `DescribeParameters` | `(ctx, groupName) ([]Parameter, error)` |

### Subnet Groups

| Operation | Signature |
|-----------|-----------|
| `CreateSubnetGroup` | `(ctx, CreateSubnetGroupConfig) (*SubnetGroup, error)` |
| `DescribeSubnetGroups` | `(ctx, names) ([]SubnetGroup, error)` |
| `UpdateSubnetGroup` | `(ctx, UpdateSubnetGroupConfig) (*SubnetGroup, error)` |
| `DeleteSubnetGroup` | `(ctx, name) (*SubnetGroup, error)` |

### Snapshots

| Operation | Signature |
|-----------|-----------|
| `CreateSnapshot` | `(ctx, CreateSnapshotConfig) (*Snapshot, error)` |
| `DescribeSnapshots` | `(ctx, names, clusterName) ([]Snapshot, error)` |
| `CopySnapshot` | `(ctx, CopySnapshotConfig) (*Snapshot, error)` |
| `DeleteSnapshot` | `(ctx, name) (*Snapshot, error)` |

### Tags & Catalogs

| Operation | Signature |
|-----------|-----------|
| `TagResource` | `(ctx, arn, tags) ([]Tag, error)` |
| `UntagResource` | `(ctx, arn, keys) ([]Tag, error)` |
| `ListTags` | `(ctx, arn) ([]Tag, error)` |
| `DescribeEngineVersions` | `(ctx, engine, version) ([]EngineVersionInfo, error)` |
| `DescribeEvents` | `(ctx) ([]Event, error)` |

### Multi-Region Clusters (optional capability — `MultiRegion`)

| Operation | Signature |
|-----------|-----------|
| `CreateMultiRegionCluster` | `(ctx, CreateMultiRegionClusterConfig) (*MultiRegionCluster, error)` |
| `DescribeMultiRegionClusters` | `(ctx, names) ([]MultiRegionCluster, error)` |
| `UpdateMultiRegionCluster` | `(ctx, name, nodeType, engineVersion, shardCount) (*MultiRegionCluster, error)` |
| `DeleteMultiRegionCluster` | `(ctx, name) (*MultiRegionCluster, error)` |
| `ListAllowedMultiRegionClusterUpdates` | `(ctx, name) ([]string, error)` |
| `DescribeMultiRegionParameterGroups` | `(ctx, names) ([]MultiRegionParameterGroup, error)` |
| `DescribeMultiRegionParameters` | `(ctx, groupName) ([]Parameter, error)` |

### Reserved Nodes (optional capability — `ReservedNodes`)

| Operation | Signature |
|-----------|-----------|
| `DescribeReservedNodes` | `(ctx) ([]ReservedNode, error)` |
| `DescribeReservedNodesOfferings` | `(ctx) ([]ReservedNodesOffering, error)` |
| `PurchaseReservedNodesOffering` | `(ctx, offeringID, reservationID, count) (*ReservedNode, error)` |

### Service Updates (optional capability — `ServiceUpdates`)

| Operation | Signature |
|-----------|-----------|
| `DescribeServiceUpdates` | `(ctx, serviceUpdateName, clusterNames, status) ([]ServiceUpdate, error)` |
| `BatchUpdateCluster` | `(ctx, clusterNames, serviceUpdateName) (processed []Cluster, unprocessed []UnprocessedCluster, error)` |

`BatchUpdateCluster` applies a service update to each named cluster; a name that
does not exist is returned in `unprocessed` (with `ClusterNotFoundFault`) rather
than failing the whole batch — matching AWS's partial-success semantics.

The three optional interfaces are AWS-only concepts, discovered by type
assertion.

**Pagination:** every `Describe*` operation honors `MaxResults`/`NextToken`.
The server pages the deterministic (sorted) result set and returns an opaque
base64 offset token; a malformed token yields `InvalidParameterValueException`.

**Total: 33 operations (+13 optional)**

---

## 11b. Keyspaces (AWS)

**Driver interface:** `services/keyspaces/driver/driver.go`
**AWS:** Amazon Keyspaces (for Apache Cassandra) | **Azure:** — | **GCP:** —

A managed, Cassandra-compatible wide-column service. Control-plane only (CQL
data operations are out of scope), so it has its own driver rather than reusing
the relational/cache drivers. Served as AWS JSON 1.0 on the `KeyspacesService.`
target prefix (`server/aws/keyspaces`); a real
`aws-sdk-go-v2/service/keyspaces` client with a custom endpoint works unchanged.
Because Keyspaces models its members in lowerCamelCase, the server lowercases
response keys so the SDK's case-sensitive deserializer decodes them.

### Keyspaces

| Operation | Signature |
|-----------|-----------|
| `CreateKeyspace` | `(ctx, CreateKeyspaceConfig) (*Keyspace, error)` |
| `GetKeyspace` | `(ctx, name) (*Keyspace, error)` |
| `ListKeyspaces` | `(ctx) ([]Keyspace, error)` |
| `UpdateKeyspace` | `(ctx, name, addRegions) (*Keyspace, error)` |
| `DeleteKeyspace` | `(ctx, name) error` |

Single- or multi-region replication (`ReplicationSpecification`); a keyspace
must be empty to delete.

### Tables

| Operation | Signature |
|-----------|-----------|
| `CreateTable` | `(ctx, CreateTableConfig) (*Table, error)` |
| `GetTable` | `(ctx, keyspace, table) (*Table, error)` |
| `ListTables` | `(ctx, keyspace) ([]Table, error)` |
| `UpdateTable` | `(ctx, UpdateTableConfig) (*Table, error)` |
| `DeleteTable` | `(ctx, keyspace, table) error` |
| `RestoreTable` | `(ctx, RestoreTableConfig) (*Table, error)` |

Full `SchemaDefinition` (partition/clustering keys, static & regular columns),
`CapacitySpecification` (PAY_PER_REQUEST / PROVISIONED + RCU/WCU), encryption,
point-in-time recovery, TTL, client-side timestamps, CDC, comment, and
multi-region replica specs. `RestoreTable` is point-in-time recovery into a new
table.

### User-Defined Types

| Operation | Signature |
|-----------|-----------|
| `CreateType` | `(ctx, keyspace, name, fields) (*UDT, error)` |
| `GetType` | `(ctx, keyspace, name) (*UDT, error)` |
| `ListTypes` | `(ctx, keyspace) ([]UDT, error)` |
| `DeleteType` | `(ctx, keyspace, name) (*UDT, error)` |

### Tags

| Operation | Signature |
|-----------|-----------|
| `TagResource` | `(ctx, arn, tags) error` |
| `UntagResource` | `(ctx, arn, keys) error` |
| `ListTagsForResource` | `(ctx, arn) ([]Tag, error)` |

### Auto Scaling (optional capability — `AutoScaling`)

| Operation | Signature |
|-----------|-----------|
| `GetTableAutoScalingSettings` | `(ctx, keyspace, table) (*Table, error)` |

Target-tracking auto scaling for PROVISIONED tables, discovered by type
assertion; errors for PAY_PER_REQUEST tables (matching AWS).

**Pagination:** `ListKeyspaces`/`ListTables`/`ListTypes`/`ListTagsForResource`
honor `MaxResults`/`NextToken` (server-side opaque base64 offset token over the
deterministic result set; a malformed token yields `ValidationException`).

**Total: 18 operations (+1 optional)**

---

## 11c. Managed Cassandra (Azure)

**Driver interface:** `services/managedcassandra/driver/driver.go`
**AWS:** — | **Azure:** Azure Managed Instance for Apache Cassandra | **GCP:** —

A managed, Cassandra-compatible cluster service under Cosmos DB. Control-plane
only (CQL is out of scope), so it has its own driver. Served as ARM REST/JSON
under `Microsoft.DocumentDB/cassandraClusters` (`server/azure/managedcassandra`);
a real `armcosmos` `CassandraClusters`/`CassandraDataCenters` client with a
custom endpoint works unchanged. Mutating ops complete synchronously so the
SDK's LRO pollers terminate on the first response (create/patch return the
resource; delete → 204; deallocate/start → 202 + `Azure-AsyncOperation`;
invokeCommand → 202 + `Location` returning the command output).

### Clusters

| Operation | Signature |
|-----------|-----------|
| `CreateOrUpdateCluster` | `(ctx, CreateClusterConfig) (*Cluster, error)` |
| `GetCluster` | `(ctx, resourceGroup, name) (*Cluster, error)` |
| `ListClustersByResourceGroup` | `(ctx, resourceGroup) ([]Cluster, error)` |
| `ListClustersBySubscription` | `(ctx) ([]Cluster, error)` |
| `UpdateCluster` | `(ctx, resourceGroup, name, ClusterPatch) (*Cluster, error)` |
| `DeleteCluster` | `(ctx, resourceGroup, name) error` |
| `DeallocateCluster` | `(ctx, resourceGroup, name) (*Cluster, error)` |
| `StartCluster` | `(ctx, resourceGroup, name) (*Cluster, error)` |
| `InvokeCommand` | `(ctx, resourceGroup, name, command, host) (string, error)` |
| `ClusterStatus` | `(ctx, resourceGroup, name) (*ClusterStatus, error)` |

Single/multi-region-capable clusters with cassandra version, delegated subnet,
authentication method, repair, backups, seed/gossip/client certs, and a
deallocate/start lifecycle. `ClusterStatus` reports per-node health;
`InvokeCommand` runs a maintenance command (e.g. `nodetool`).

### Data Centers

| Operation | Signature |
|-----------|-----------|
| `CreateOrUpdateDataCenter` | `(ctx, CreateDataCenterConfig) (*DataCenter, error)` |
| `GetDataCenter` | `(ctx, resourceGroup, cluster, name) (*DataCenter, error)` |
| `ListDataCenters` | `(ctx, resourceGroup, cluster) ([]DataCenter, error)` |
| `UpdateDataCenter` | `(ctx, resourceGroup, cluster, name, DataCenterPatch) (*DataCenter, error)` |
| `DeleteDataCenter` | `(ctx, resourceGroup, cluster, name) error` |

Datacenters live under a cluster (node count, disk capacity, SKU, availability
zone, delegated subnet, seed nodes). Deleting a cluster cascade-deletes its
datacenters; creating a datacenter validates the parent cluster exists;
deallocate/start propagate to all datacenters.

**Total: 15 operations**

---

## 11d. Bigtable (GCP)

**Driver interface:** `services/bigtable/driver/driver.go`
**AWS:** — | **Azure:** — | **GCP:** Cloud Bigtable

A wide-column NoSQL database. Control-plane only (the data plane is out of
scope), so it has its own driver. Served as GCP REST/JSON under `/v2/...`
(`server/gcp/bigtable`); a real `google.golang.org/api/bigtableadmin/v2` client
with a custom endpoint works unchanged. The wire layer uses the SDK's own types
for exact fidelity. Long-running RPCs return a Google `Operation{done:true}`
carrying the resulting resource, and `operations.get` returns a done Operation,
so SDK LRO waits complete.

### Instances

| Operation | Signature |
|-----------|-----------|
| `CreateInstance` | `(ctx, CreateInstanceConfig) (*Instance, *Operation, error)` |
| `GetInstance` | `(ctx, name) (*Instance, error)` |
| `ListInstances` | `(ctx, project) ([]Instance, error)` |
| `UpdateInstance` | `(ctx, name, cfg) (*Instance, error)` |
| `PartialUpdateInstance` | `(ctx, name, cfg) (*Instance, *Operation, error)` |
| `DeleteInstance` | `(ctx, name) error` |

### Clusters

| Operation | Signature |
|-----------|-----------|
| `CreateCluster` | `(ctx, CreateClusterConfig) (*Cluster, *Operation, error)` |
| `GetCluster` | `(ctx, name) (*Cluster, error)` |
| `ListClusters` | `(ctx, instance) ([]Cluster, error)` |
| `UpdateCluster` | `(ctx, name, serveNodes, autoscaling) (*Cluster, *Operation, error)` |
| `DeleteCluster` | `(ctx, name) error` |
| `GetClusterMemoryLayer` | `(ctx, name) error` |

### Tables

| Operation | Signature |
|-----------|-----------|
| `CreateTable` | `(ctx, CreateTableConfig) (*Table, error)` |
| `GetTable` / `ListTables` | `(ctx, name) / (ctx, instance)` |
| `UpdateTable` | `(ctx, name, deletionProtection) (*Table, *Operation, error)` |
| `DeleteTable` / `UndeleteTable` | `(ctx, name)` (soft-delete + restore) |
| `ModifyColumnFamilies` | `(ctx, name, mods) (*Table, error)` |
| `DropRowRange` / `GenerateConsistencyToken` / `CheckConsistency` | data-consistency helpers |
| `RestoreTable` | `(ctx, parent, tableID, backup) (*Table, *Operation, error)` |

Column families carry recursive GC rules (`maxNumVersions` / `maxAge` /
`union` / `intersection`).

### App Profiles

| Operation | Signature |
|-----------|-----------|
| `CreateAppProfile` / `GetAppProfile` / `ListAppProfiles` | routing-policy CRUD |
| `UpdateAppProfile` | `(ctx, name, cfg) (*AppProfile, *Operation, error)` |
| `DeleteAppProfile` | `(ctx, name) error` |

### Backups

| Operation | Signature |
|-----------|-----------|
| `CreateBackup` | `(ctx, CreateBackupConfig) (*Backup, *Operation, error)` |
| `GetBackup` / `ListBackups` / `UpdateBackup` / `DeleteBackup` | backup CRUD |
| `CopyBackup` | `(ctx, CopyBackupConfig) (*Backup, *Operation, error)` |

### Operations & IAM

`GetOperation` (LRO poll) plus per-resource IAM on instances, tables, and
backups: `GetIamPolicy`, `SetIamPolicy`, `TestIamPermissions`.

Modeling: instances own clusters/tables/app-profiles (parent linkage, cascade
delete); backups live under a cluster and restore into a new table; serve-node
counts are bounded; clone-on-read on every path.

**Total: 38 operations**

---

## 11e. Cosmos DB for PostgreSQL (Azure)

**Driver interface:** `services/cosmospostgresql/driver/driver.go`
**Azure:** Cosmos DB for PostgreSQL (Citus) — `Microsoft.DBforPostgreSQL/serverGroupsv2`

Real `armcosmosforpostgresql` clients configured with a custom endpoint hit the
ARM handler (`server/azure/cosmospostgresql`) the same way they hit
management.azure.com. Create/update RPCs return the resource inline with a
terminal `provisioningState`; the cluster start/stop/restart/promote actions
reply `202` + `Location` and the poller reads a terminal status from the
`operationStatuses` URL.

### Clusters (server groups)

| Operation | Signature |
|-----------|-----------|
| `CreateOrUpdateCluster` | `(ctx, CreateClusterConfig) (*Cluster, error)` |
| `GetCluster` / `ListClustersByResourceGroup` / `ListClustersBySubscription` | cluster reads |
| `UpdateCluster` | `(ctx, rg, name, ClusterPatch) (*Cluster, error)` (PATCH) |
| `DeleteCluster` | `(ctx, rg, name) error` |
| `RestartCluster` / `StartCluster` / `StopCluster` | lifecycle actions (LRO) |
| `PromoteReadReplica` | `(ctx, rg, name) error` — detach a replica |
| `CheckNameAvailability` | `(ctx, name, type) (*NameAvailability, error)` |

### Firewall Rules & Roles

| Operation | Signature |
|-----------|-----------|
| `CreateOrUpdateFirewallRule` / `GetFirewallRule` / `ListFirewallRules` / `DeleteFirewallRule` | IP allow-list CRUD |
| `CreateRole` / `GetRole` / `ListRoles` / `DeleteRole` | Postgres role CRUD |

### Servers (nodes), Configurations & Private Endpoints

| Operation | Signature |
|-----------|-----------|
| `GetServer` / `ListServers` | read-only derived nodes (coordinator + workers) |
| `ListConfigurations` / `GetConfiguration` | cluster-wide server parameters (per-role values) |
| `GetCoordinatorConfiguration` / `GetNodeConfiguration` / `ListServerConfigurations` | server-scoped parameter reads |
| `UpdateCoordinatorConfiguration` / `UpdateNodeConfiguration` | per-role parameter updates (LRO) |
| `CreateOrUpdatePrivateEndpointConnection` / `GetPrivateEndpointConnection` / `ListPrivateEndpointConnections` / `DeletePrivateEndpointConnection` | private-endpoint CRUD |
| `GetPrivateLinkResource` / `ListPrivateLinkResources` | private-link resource reads |

Modeling: a cluster owns its firewall rules, roles, configurations, and
private-endpoint connections (parent linkage, cascade delete); nodes are derived
from the cluster shape (one coordinator + N workers); read replicas link back to
a source cluster and detach on promote; clone-on-read on every path.

**Total: 34 operations**

---

## 12. Secrets

**Driver interface:** `services/secrets/driver/driver.go`
**AWS:** Secrets Manager | **Azure:** Key Vault | **GCP:** Secret Manager

### Secret Operations

| Operation | Signature |
|-----------|-----------|
| `CreateSecret` | `(ctx, config, value) (*SecretInfo, error)` |
| `DeleteSecret` | `(ctx, name) error` |
| `GetSecret` | `(ctx, name) (*SecretInfo, error)` |
| `ListSecrets` | `(ctx) ([]SecretInfo, error)` |

### Secret Versions

| Operation | Signature |
|-----------|-----------|
| `PutSecretValue` | `(ctx, name, value) (*SecretVersion, error)` |
| `GetSecretValue` | `(ctx, name, versionID) (*SecretVersion, error)` |
| `ListSecretVersions` | `(ctx, name) ([]SecretVersion, error)` |

**Total: 7 operations**

---

## 13. Logging

**Driver interface:** `services/logging/driver/driver.go`
**AWS:** CloudWatch Logs | **Azure:** Log Analytics | **GCP:** Cloud Logging

### Log Group Operations

| Operation | Signature |
|-----------|-----------|
| `CreateLogGroup` | `(ctx, config) (*LogGroupInfo, error)` |
| `DeleteLogGroup` | `(ctx, name) error` |
| `GetLogGroup` | `(ctx, name) (*LogGroupInfo, error)` |
| `ListLogGroups` | `(ctx) ([]LogGroupInfo, error)` |

### Log Stream Operations

| Operation | Signature |
|-----------|-----------|
| `CreateLogStream` | `(ctx, logGroup, streamName) (*LogStreamInfo, error)` |
| `DeleteLogStream` | `(ctx, logGroup, streamName) error` |
| `ListLogStreams` | `(ctx, logGroup) ([]LogStreamInfo, error)` |

### Log Event Operations

| Operation | Signature |
|-----------|-----------|
| `PutLogEvents` | `(ctx, logGroup, streamName, events) error` |
| `GetLogEvents` | `(ctx, input) ([]LogEvent, error)` |

### Filtering and Metric Filters

| Operation | Signature |
|-----------|-----------|
| `FilterLogEvents` | `(ctx, input) ([]FilteredLogEvent, error)` |
| `PutMetricFilter` | `(ctx, config) error` |
| `DeleteMetricFilter` | `(ctx, logGroup, filterName) error` |
| `DescribeMetricFilters` | `(ctx, logGroup) ([]MetricFilterInfo, error)` |

**Total: 13 operations**

---

## 14. Notification

**Driver interface:** `services/notification/driver/driver.go`
**AWS:** SNS | **Azure:** Notification Hubs | **GCP:** FCM

### Topic Operations

| Operation | Signature |
|-----------|-----------|
| `CreateTopic` | `(ctx, config) (*TopicInfo, error)` |
| `DeleteTopic` | `(ctx, id) error` |
| `GetTopic` | `(ctx, id) (*TopicInfo, error)` |
| `ListTopics` | `(ctx) ([]TopicInfo, error)` |

### Subscription Operations

| Operation | Signature |
|-----------|-----------|
| `Subscribe` | `(ctx, config) (*SubscriptionInfo, error)` |
| `Unsubscribe` | `(ctx, subscriptionID) error` |
| `ListSubscriptions` | `(ctx, topicID) ([]SubscriptionInfo, error)` |

### Publishing

| Operation | Signature |
|-----------|-----------|
| `Publish` | `(ctx, input) (*PublishOutput, error)` |

**Total: 8 operations**

---

## 15. Container Registry

**Driver interface:** `services/containerregistry/driver/driver.go`
**AWS:** ECR | **Azure:** ACR | **GCP:** Artifact Registry

### Repository Management

| Operation | Signature |
|-----------|-----------|
| `CreateRepository` | `(ctx, config) (*Repository, error)` |
| `DeleteRepository` | `(ctx, name, force) error` |
| `GetRepository` | `(ctx, name) (*Repository, error)` |
| `ListRepositories` | `(ctx) ([]Repository, error)` |

### Image Management

| Operation | Signature |
|-----------|-----------|
| `PutImage` | `(ctx, manifest) (*ImageDetail, error)` |
| `GetImage` | `(ctx, repository, reference) (*ImageDetail, error)` |
| `ListImages` | `(ctx, repository) ([]ImageDetail, error)` |
| `DeleteImage` | `(ctx, repository, reference) error` |
| `TagImage` | `(ctx, repository, sourceRef, targetTag) error` |

### Lifecycle Policies

| Operation | Signature |
|-----------|-----------|
| `PutLifecyclePolicy` | `(ctx, repository, policy) error` |
| `GetLifecyclePolicy` | `(ctx, repository) (*LifecyclePolicy, error)` |
| `EvaluateLifecyclePolicy` | `(ctx, repository) ([]string, error)` |

### Image Scanning

| Operation | Signature |
|-----------|-----------|
| `StartImageScan` | `(ctx, repository, reference) (*ScanResult, error)` |
| `GetImageScanResults` | `(ctx, repository, reference) (*ScanResult, error)` |

**Total: 14 operations**

---

## 16. Event Bus

**Driver interface:** `services/eventbus/driver/driver.go`
**AWS:** EventBridge | **Azure:** Event Grid | **GCP:** Eventarc

### Bus Management

| Operation | Signature |
|-----------|-----------|
| `CreateEventBus` | `(ctx, config) (*EventBusInfo, error)` |
| `DeleteEventBus` | `(ctx, name) error` |
| `GetEventBus` | `(ctx, name) (*EventBusInfo, error)` |
| `ListEventBuses` | `(ctx) ([]EventBusInfo, error)` |

### Rule Management

| Operation | Signature |
|-----------|-----------|
| `PutRule` | `(ctx, config) (*Rule, error)` |
| `DeleteRule` | `(ctx, eventBus, ruleName) error` |
| `GetRule` | `(ctx, eventBus, ruleName) (*Rule, error)` |
| `ListRules` | `(ctx, eventBus) ([]Rule, error)` |
| `EnableRule` | `(ctx, eventBus, ruleName) error` |
| `DisableRule` | `(ctx, eventBus, ruleName) error` |

### Target Management

| Operation | Signature |
|-----------|-----------|
| `PutTargets` | `(ctx, eventBus, ruleName, targets) error` |
| `RemoveTargets` | `(ctx, eventBus, ruleName, targetIDs) error` |
| `ListTargets` | `(ctx, eventBus, ruleName) ([]Target, error)` |

### Event Publishing

| Operation | Signature |
|-----------|-----------|
| `PutEvents` | `(ctx, events) (*PublishResult, error)` |

### Event History

| Operation | Signature |
|-----------|-----------|
| `GetEventHistory` | `(ctx, eventBus, limit) ([]Event, error)` |

**Total: 15 operations**

---

## 17. Relational Database

**Driver interface:** `services/relationaldb/driver/driver.go`
**AWS:** `rds` (covers Aurora, Neptune, and DocumentDB engines), `redshift` | **Azure:** `sql`, `postgresflex`, `mysqlflex` | **GCP:** `cloudsql`, `alloydb`

A single portable interface backs every RDBMS handler. Engine selection (MySQL / PostgreSQL / Aurora / Neptune / DocumentDB / Redshift / Cloud SQL / Azure SQL / AlloyDB / …) is a field on the input config, not a separate driver.

**AlloyDB (GCP):** a PostgreSQL-compatible managed database served on the `alloydb.googleapis.com/v1` REST API (`server/gcp/alloydb`). It reuses the relational driver — AlloyDB clusters map to `Cluster`, instances (PRIMARY / READ_POOL / SECONDARY) to `Instance`, and cluster backups to `ClusterSnapshot` — plus the `Users` and `Databases` capabilities. AlloyDB-specific behavior (instance types, machine vCPU config, cross-region secondary clusters + promote, instance failover/restart, continuous/automated backup config) lives in the optional `AlloyDB` capability. Because AlloyDB's REST paths (`/v1/projects/{p}/locations/{l}/clusters…`) are identical to GKE's, the two cannot be multiplexed on one server; the combined GCP server leaves `Drivers.AlloyDB` nil and callers inject it in place of GKE.

### Instance Operations

| Operation | Signature |
|-----------|-----------|
| `CreateInstance` | `(ctx, InstanceConfig) (*Instance, error)` |
| `DescribeInstances` | `(ctx, ids) ([]Instance, error)` |
| `ModifyInstance` | `(ctx, id, ModifyInstanceInput) (*Instance, error)` |
| `DeleteInstance` | `(ctx, id) error` |
| `StartInstance` | `(ctx, id) error` |
| `StopInstance` | `(ctx, id) error` |
| `RebootInstance` | `(ctx, id) error` |

### Cluster Operations

| Operation | Signature |
|-----------|-----------|
| `CreateCluster` | `(ctx, ClusterConfig) (*Cluster, error)` |
| `DescribeClusters` | `(ctx, ids) ([]Cluster, error)` |
| `ModifyCluster` | `(ctx, id, ModifyInstanceInput) (*Cluster, error)` |
| `DeleteCluster` | `(ctx, id) error` |
| `StartCluster` | `(ctx, id) error` |
| `StopCluster` | `(ctx, id) error` |

### Snapshot Operations

| Operation | Signature |
|-----------|-----------|
| `CreateSnapshot` | `(ctx, SnapshotConfig) (*Snapshot, error)` |
| `DescribeSnapshots` | `(ctx, ids, instanceID) ([]Snapshot, error)` |
| `DeleteSnapshot` | `(ctx, id) error` |
| `RestoreInstanceFromSnapshot` | `(ctx, RestoreInstanceInput) (*Instance, error)` |

### Cluster Snapshot Operations

| Operation | Signature |
|-----------|-----------|
| `CreateClusterSnapshot` | `(ctx, ClusterSnapshotConfig) (*ClusterSnapshot, error)` |
| `DescribeClusterSnapshots` | `(ctx, ids, clusterID) ([]ClusterSnapshot, error)` |
| `DeleteClusterSnapshot` | `(ctx, id) error` |
| `RestoreClusterFromSnapshot` | `(ctx, RestoreClusterInput) (*Cluster, error)` |

### Subnet Groups (optional capability)

DB subnet groups are an AWS concept — Azure and GCP place managed databases with
vnet integration instead. The `SubnetGroups` interface is therefore kept out of
`RelationalDB` and discovered by type assertion; drivers that do not implement it
answer `InvalidAction`.

| Operation | Signature |
|-----------|-----------|
| `CreateDBSubnetGroup` | `(ctx, SubnetGroupConfig) (*SubnetGroup, error)` |
| `DescribeDBSubnetGroups` | `(ctx, names) ([]SubnetGroup, error)` |
| `DeleteDBSubnetGroup` | `(ctx, name) error` |

`VPCID` is derived from the member subnets rather than supplied by the caller,
matching the real service. Callers tearing down a VPC list subnet groups and
match on it.

### Parameter Groups (optional capability — `ParameterGroups`)

DB and DB **cluster** parameter groups. Only user-set parameters are modeled;
the emulator does not fabricate the hundreds of engine defaults real AWS
returns. Real AWS reuses the `DBParameterGroup*` fault codes for the cluster
variants, so error mapping is shared.

| Operation | Signature |
|-----------|-----------|
| `CreateDBParameterGroup` | `(ctx, ParameterGroupConfig) (*ParameterGroup, error)` |
| `DescribeDBParameterGroups` | `(ctx, names) ([]ParameterGroup, error)` |
| `ModifyDBParameterGroup` | `(ctx, name, []Parameter) (*ParameterGroup, error)` |
| `DeleteDBParameterGroup` | `(ctx, name) error` |
| `DescribeDBParameters` | `(ctx, name) ([]Parameter, error)` |
| `ResetDBParameterGroup` | `(ctx, name, params, resetAll) (*ParameterGroup, error)` |
| `CopyDBParameterGroup` | `(ctx, source, target, description) (*ParameterGroup, error)` |
| `CreateDBClusterParameterGroup` … `CopyDBClusterParameterGroup` | cluster-scoped analogues (7) |

### Option Groups (optional capability — `OptionGroups`)

| Operation | Signature |
|-----------|-----------|
| `CreateOptionGroup` | `(ctx, OptionGroupConfig) (*OptionGroup, error)` |
| `DescribeOptionGroups` | `(ctx, names, engineName) ([]OptionGroup, error)` |
| `ModifyOptionGroup` | `(ctx, name, include, remove) (*OptionGroup, error)` |
| `DeleteOptionGroup` | `(ctx, name) error` |
| `CopyOptionGroup` | `(ctx, source, target, description) (*OptionGroup, error)` |
| `DescribeOptionGroupOptions` | `(ctx, engineName, majorEngineVersion) ([]OptionGroupOption, error)` |

`DescribeOptionGroupOptions` returns a representative per-engine catalog of
well-known option names, not AWS's exhaustive version-specific list.

### Read Replicas (optional capability — `ReadReplicas`)

| Operation | Signature |
|-----------|-----------|
| `CreateDBInstanceReadReplica` | `(ctx, ReadReplicaConfig) (*Instance, error)` |
| `PromoteReadReplica` | `(ctx, id) (*Instance, error)` |

A replica inherits its source's engine/version/storage; the source tracks its
replica IDs and the replica records its source. Promotion detaches it.

### Snapshot Copy & Point-in-Time Restore (optional capability — `AdvancedRestore`)

| Operation | Signature |
|-----------|-----------|
| `CopyDBSnapshot` | `(ctx, source, target, tags) (*Snapshot, error)` |
| `CopyDBClusterSnapshot` | `(ctx, source, target, tags) (*ClusterSnapshot, error)` |
| `RestoreDBInstanceToPointInTime` | `(ctx, RestoreInstanceToPointInTimeInput) (*Instance, error)` |
| `RestoreDBClusterToPointInTime` | `(ctx, RestoreClusterToPointInTimeInput) (*Cluster, error)` |

The emulator retains no historical timeline, so PITR clones the source's
current spec; `RestoreTime` / `UseLatestRestorableTime` are accepted but not
replayed.

### RDS Proxy (optional capability — `DBProxies`)

A proxy has a single implicit `default` target group; targets are RDS instances
(`RDS_INSTANCE`) or clusters (`TRACKED_CLUSTER`), validated on registration.

| Operation | Signature |
|-----------|-----------|
| `CreateDBProxy` / `DescribeDBProxies` / `ModifyDBProxy` / `DeleteDBProxy` | proxy lifecycle (4) |
| `RegisterDBProxyTargets` / `DeregisterDBProxyTargets` / `DescribeDBProxyTargets` | target membership (3) |
| `DescribeDBProxyTargetGroups` | `(ctx, name) ([]ProxyTargetGroup, error)` |

### Event Subscriptions (optional capability — `EventSubscriptions`)

| Operation | Signature |
|-----------|-----------|
| `CreateEventSubscription` / `DescribeEventSubscriptions` / `ModifyEventSubscription` / `DeleteEventSubscription` | subscription CRUD (4) |
| `DescribeEvents` | `(ctx, sourceType, sourceID, categories) ([]Event, error)` — empty: no event timeline is retained |
| `DescribeEventCategories` | `(ctx, sourceType) ([]EventCategoryGroup, error)` |

### Aurora Cluster Endpoints, Failover & Global Clusters

- `ClusterEndpoints`: `CreateDBClusterEndpoint` / `DescribeDBClusterEndpoints` / `ModifyDBClusterEndpoint` / `DeleteDBClusterEndpoint` (4).
- `ClusterFailover`: `FailoverDBCluster` promotes the target member to writer, or rotates the first reader when no target is given (1).
- `GlobalClusters`: `CreateGlobalCluster` / `DescribeGlobalClusters` / `ModifyGlobalCluster` / `DeleteGlobalCluster` / `RemoveFromGlobalCluster` (5).

### Metadata & Tagging

- `Metadata`: `DescribeDBEngineVersions`, `DescribeOrderableDBInstanceOptions` — representative per-engine catalogs (2).
- `Tagging`: `AddTagsToResource`, `RemoveTagsFromResource`, `ListTagsForResource` — addressed by resource ARN over the tag-bearing stores (instances, clusters, instance/cluster snapshots) (3).

### Azure & GCP managed-SQL native sub-resources (optional capabilities)

Each managed-SQL service also exposes its cloud's own child resources and
actions. Like the RDS capabilities above, these are kept out of the core
`RelationalDB` interface and discovered by type assertion, so a driver only
answers for the resources its cloud actually has; others return `InvalidAction`.
The server handlers reach them the way real SDK clients do (ARM sub-resource
routes for Azure, sqladmin sub-collections for Cloud SQL), and the mocks
cascade-delete children when their parent server/instance is deleted.

| Capability | Operations | Implemented by |
|-----------|-----------|----------------|
| `Databases` | Create / Get / List / Delete | `mysqlflex`, `postgresflex`, `cloudsql`, `alloydb` |
| `FirewallRules` | Create / Get / List / Delete | `mysqlflex`, `postgresflex`, `sql` |
| `Configurations` | Set / Get / List (server parameters) | `mysqlflex`, `postgresflex` |
| `Failover` | `FailoverInstance` | `mysqlflex`, `cloudsql` |
| `VNetRules` | Create / Get / List / Delete | `sql` |
| `ElasticPools` | Create / Get / List / Delete | `sql` |
| `FailoverGroups` | Create / Get / List / Delete / Failover | `sql` |
| `AADAdmins` | Set / Get / List / Delete | `sql` |
| `Users` | Create / Get / List / Update / Delete | `cloudsql`, `alloydb` |
| `SslCerts` | Create / Get / List / Delete | `cloudsql` |
| `Clonable` | `CloneInstance` | `cloudsql` |
| `ReplicaPromotion` | `PromoteReplica` | `cloudsql` |
| `ManagedInstances` | managed-instance CRUD + Start/Stop/Failover, managed-database CRUD/List | `sql` |
| `AlloyDB` | AlloyDB cluster/instance create, CreateSecondary/Promote, instance Failover/Restart, `*Info` accessors | `alloydb` |

Cloud SQL also serves the `startReplica`/`stopReplica` instance actions (mapped
onto Start/Stop) and the static `tiers` (`/v1/projects/{p}/tiers`) and `flags`
(`/v1/flags`) reference catalogs. Azure SQL adds the SQL Managed Instance family
(`Microsoft.Sql/managedInstances` + managed databases) alongside the
single-database logical server. Managed relational servers surface in
cross-service discovery (Azure Resource Graph as `microsoft.sql/servers`,
`microsoft.dbformysql/flexibleservers`,
`microsoft.dbforpostgresql/flexibleservers`; GCP Cloud Asset as
`sqladmin.googleapis.com/Instance`), are billed per instance-hour via the
`relationaldb:*` cost catalog, and emit their cloud's monitoring metrics
(including the `Microsoft.Sql/servers/elasticpools` pool namespace).

**Total: 21 core operations + 109 optional across 25 type-asserted capability
interfaces** — the 12 RDS-oriented ones (`SubnetGroups`, `ParameterGroups`,
`OptionGroups`, `ReadReplicas`, `AdvancedRestore`, `DBProxies`,
`EventSubscriptions`, `ClusterEndpoints`, `ClusterFailover`, `GlobalClusters`,
`Metadata`, `Tagging`) plus the 13 Azure/GCP managed-SQL ones (`Databases`,
`FirewallRules`, `Configurations`, `Failover`, `VNetRules`, `ElasticPools`,
`FailoverGroups`, `AADAdmins`, `Users`, `SslCerts`, `Clonable`,
`ReplicaPromotion`, `ManagedInstances`). Each cloud implements the subset that
maps to a real resource and answers `InvalidAction` otherwise.

---

## 18. Kubernetes

**Control plane:** AWS `eks`, Azure `aks`, GCP `gke` — cluster, node-pool, and addon / Fargate-profile / maintenance-config lifecycle, driven by the real cloud SDKs.
**Data plane:** shared `services/kubernetes/` package — an in-memory Kubernetes API server registered by every cluster across all three providers. Kubeconfigs returned by the control plane point at `<base>/k8s/<cluster-uid>` so `client-go` and `kubectl` operate end-to-end.

Each provider exposes its native control-plane API. The data plane has no portable driver — clients connect via the kubeconfig the control plane hands out, then talk standard Kubernetes REST.

### AWS EKS (`providers/aws/eks`)

| Resource | Operations |
|----------|-----------|
| Clusters | CreateCluster, DescribeCluster, ListClusters, UpdateClusterConfig, UpdateClusterVersion, DeleteCluster |
| Node Groups | CreateNodegroup, DescribeNodegroup, ListNodegroups, UpdateNodegroupConfig, UpdateNodegroupVersion, DeleteNodegroup |
| Fargate Profiles | CreateFargateProfile, DescribeFargateProfile, ListFargateProfiles, DeleteFargateProfile |
| Addons | CreateAddon, DescribeAddon, ListAddons, UpdateAddon, DeleteAddon |

Operations: **21**

### Azure AKS (`providers/azure/aks`)

| Resource | Operations |
|----------|-----------|
| Managed Clusters | CreateOrUpdateCluster, GetCluster, UpdateClusterTags, DeleteCluster, ListClusters, ListClustersByResourceGroup, RotateClusterCertificates |
| Agent Pools | CreateOrUpdateAgentPool, GetAgentPool, DeleteAgentPool, ListAgentPools |
| Maintenance Configs | CreateOrUpdateMaintenanceConfig, GetMaintenanceConfig, DeleteMaintenanceConfig, ListMaintenanceConfigs |
| Credentials | `ListClusterAdminCredentials`, `ListClusterUserCredentials`, `ListClusterMonitoringUserCredentials` — return a kubeconfig pointing at the in-memory data plane (or the `*-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local` sentinel when no APIServer is wired) |

Operations: **18**

### GCP GKE (`providers/gcp/gke`)

| Resource | Operations |
|----------|-----------|
| Clusters | CreateCluster, GetCluster, ListClusters, UpdateCluster, DeleteCluster, SetClusterLogging, SetClusterMonitoring, SetMasterAuth, SetLegacyAbac, SetNetworkPolicy, SetMaintenancePolicy, SetResourceLabels, StartIPRotation, CompleteIPRotation |
| Node Pools | CreateNodePool, GetNodePool, ListNodePools, UpdateNodePool, DeleteNodePool, SetNodePoolSize, SetNodePoolAutoscaling, SetNodePoolManagement, RollbackNodePool |
| Operations | GetOperation, ListOperations, CancelOperation |

Operations: **26**

### Data plane (`services/kubernetes/`)

Shared in-memory K8s API server registered by every cluster from any provider. URL: `<base>/k8s/<cluster-uid>/...`. Served over **real TLS**: the control plane advertises a shared CA (`internal/k8spki`) that certifies the serving cert, so `client-go` and `kubectl` validate the connection normally — kubeconfigs carry `certificate-authority-data`, not `insecure-skip-tls-verify`.

**Real `kubectl` works end-to-end**, not just `client-go`: the server decodes the **protobuf** request bodies kubectl sends on writes (it accepts protobuf and replies JSON, which kubectl's `Accept` allows), and serves an **OpenAPI v3** discovery document (plus a protobuf **v2** for the legacy path) carrying every served GVK so `kubectl apply` validation passes. Verified against `kubectl` v1.36 across all three providers: `create/apply/scale/set image/patch/rollout/delete`, `get` with short names (`pvc`, `hpa`, `sts`, …), and cascade teardown.

It behaves like a tiny always-converged cluster (minikube-like) rather than a bare object store: a **synchronous reconcile engine** runs on every write — there are no controller goroutines, so results are immediate and deterministic. Controllers materialize Running Pods, Services get Endpoints, PVCs bind, Jobs complete.

**Discovery** is derived from the resource registry (`registeredResources()`), so `/api`, `/apis`, and every `/apis/<group>/<version>` list exactly the resources the server serves — discovery can't promise a kind that 404s.

**Core (`core/v1`)**: Namespace, ConfigMap, Secret (StringData merged into Data), ServiceAccount (`default` auto-created per namespace), Pod (driven **Running** with a synthetic Pod IP — a directly-created Pod with a terminal phase is preserved), Service (ClusterIP from 10.96.0.0/12, immutable on update), Endpoints (get/list/watch only — auto-managed per Service), PersistentVolumeClaim (→ Bound), PersistentVolume (→ Available), Node, Event, ResourceQuota, LimitRange.

**Workload controllers (`apps/v1`)**: Deployment, ReplicaSet, StatefulSet (stable `-0..-N` names + one Bound PVC per `volumeClaimTemplate`), DaemonSet (one Pod per node whose labels satisfy the template `nodeSelector` — zero Pods when it doesn't match the synthetic node). A **Deployment interposes a ReplicaSet** per pod-template revision (Deployment→RS→Pod, matching real topology), and a template change creates a new ReplicaSet and deletes the old one outright — an instantaneous swap (no `revisionHistoryLimit`, no `kubectl rollout undo`, no surge/unavailable pacing). All materialize Running Pods owned via `ownerReferences`; deleting a controller cascade-deletes the chain and drains Endpoints. Deployments/StatefulSets expose **`/scale`** and **`/status`** subresources. CronJob scheduling is driven explicitly via `TickCronJobs()` (no background timer), which performs real due-evaluation against the cluster clock: it parses the standard 5-field `spec.schedule` (`*`, `*/n`, lists, `a-b` ranges) and materializes a Job only when a scheduled time falls in `(status.lastScheduleTime, now]` — advancing `lastScheduleTime` to the fired slot so re-ticking the same instant never double-creates — and honors `concurrencyPolicy` (`Forbid`/`Replace`/`Allow`) and `startingDeadlineSeconds`.

**Other groups** (registry-backed CRUD + list/watch/patch/delete): `batch/v1` Job (→ Succeeded Pods) / CronJob; `networking.k8s.io/v1` Ingress (→ load-balancer IP) / IngressClass / NetworkPolicy; `rbac.authorization.k8s.io/v1` Role / RoleBinding / ClusterRole / ClusterRoleBinding; `storage.k8s.io/v1` StorageClass; `autoscaling/v2` HorizontalPodAutoscaler; `discovery.k8s.io/v1` EndpointSlice; `policy/v1` PodDisruptionBudget; `apiextensions.k8s.io/v1` CustomResourceDefinition; `admissionregistration.k8s.io/v1` Mutating/ValidatingWebhookConfiguration.

**Custom resources (CRDs)**: creating a `CustomResourceDefinition` dynamically materializes a servable store for every served version — the custom-resource kind is then served by the generic handler (CRUD/list/watch/`/status`) and advertised in discovery immediately; the CRD is marked `Established`. Deleting the CRD deregisters the kind and cascade-deletes its custom resources — including when the CRD carries a finalizer, in which case teardown runs once the last finalizer drains. Structural schema validation of CRs is a documented simplification (accept-and-store).

**Selectors & pagination**: label selectors on list; field selectors for `metadata.name` / `metadata.namespace`, Pod `status.phase` / `spec.nodeName`, and Event fields (`involvedObject.name/namespace/kind/uid`, `reason`, `type`). List responses honor **`?limit=&continue=`** chunked pagination across the registry and typed list paths: the `metadata.continue` token is key-anchored (it encodes the last object's `namespace/name`), so an insert or delete before that key cannot skip or duplicate later items under concurrent mutation, and a malformed token returns `410 Gone` (reason `Expired`) per client-go's pager contract. A well-formed token whose key was since deleted resumes gracefully at the next greater key rather than `410`-ing on a compacted resourceVersion — strictly more forgiving than upstream.

**Patch & server-side apply**: JSON-merge-patch, JSONPatch (RFC 6902), and strategic-merge-patch (real strategic merge against the typed struct for core/apps kinds, so `kubectl set image` merges the container list by name). **Server-side apply** (`application/apply-patch+yaml`) tracks per-`fieldManager` field ownership in `metadata.managedFields`; an apply that changes a field owned by another manager returns **409 Conflict** unless `?force=true` (which transfers ownership), and an owner re-applying the same value is a no-op. A re-apply by the same manager that omits a field it previously owned removes that field, unless another manager also owns it. Plain PUT/PATCH updates record an `Update`-operation `managedFields` entry for their `fieldManager` (defaulted from the User-Agent when absent), taking or sharing ownership rather than conflicting (only Apply-vs-Apply is a 409). Ownership is tracked at leaf granularity (map keys / whole arrays) — per-element list merging is not modeled, a documented subset of upstream SSA.

**Dry-run**: writes with `?dryRun=All` (`kubectl apply|create|delete --dry-run=server`) run validation, defaulting, and quota admission (a create against an at-limit namespace returns the same `403` a real create would), echo the object the server would store, and persist nothing — no resourceVersion bump, reconcile, quota reservation, or watch event.

**Finalizers**: an object carrying `metadata.finalizers` goes **Terminating** on delete (`deletionTimestamp` stamped, object retained) and is removed only when the last finalizer is cleared via update/patch — on the registry path and typed Namespace/Pod. Finalizers are also honored during cascade: a finalizer-bearing child reached by owner garbage-collection or namespace teardown goes Terminating rather than being reaped, until its finalizers drain. The server-owned `deletionTimestamp` survives a merge-patch — an RFC-7396 `null` cannot resurrect a Terminating object.

**Pod subresources**: `pods/{name}/log` returns synthetic container output; `exec`/`attach`/`portforward` return a typed `501` (they need a streaming protocol upgrade the emulator doesn't implement); `pods/{name}/eviction` honors PodDisruptionBudgets.

**Metrics & autoscaling**: `metrics.k8s.io/v1beta1` (`kubectl top`) serves synthetic Pod/Node metrics from the live pods + synthetic node; a HorizontalPodAutoscaler reconcile drives its target Deployment on a Resource CPU `averageUtilization` metric — sampling the target Pods' CPU from that metrics source and applying the real HPA ratio `desiredReplicas = ceil(currentReplicas × currentUtilization ÷ targetUtilization)`, clamped into `[minReplicas, maxReplicas]` — and falls back to a plain min/max clamp when no CPU metric is configured or the target Pods declare no CPU request, reporting `currentReplicas`/`desiredReplicas`/`currentMetrics` on status.

**Policy enforcement**: object-count **ResourceQuota** is enforced on create (403 over limit) and on server-side dry-run; `status.used` is updated on create and recomputed from the live count on delete (it tracks the live object count rather than climbing monotonically); **LimitRange** applies container defaults and min/max validation on pod create; **PodDisruptionBudget** gates `pods/eviction` (429 when eviction would violate the budget); **RBAC** is queryable via `authorization.k8s.io/v1` SubjectAccessReview (evaluated against stored Roles/ClusterRoles + bindings); **NetworkPolicy** is queryable via an in-process evaluation (no live traffic).

**Admission webhooks** (opt-in): Mutating/ValidatingWebhookConfiguration objects store and round-trip through `kubectl apply`. With admission explicitly enabled (`APIServer.SetAdmissionEnabled`), create/update/patch calls matching webhooks apply mutations and honor denials (4xx); it is off by default so the data plane stays zero-network and deterministic.

**Watch resume**: a watch with `resourceVersion>0` skips the initial snapshot replay and streams only subsequent events; `allowWatchBookmarks=true` emits a post-sync BOOKMARK carrying the current resourceVersion. A slow watcher that overflows its buffer gets a `410 Gone` so `client-go` relists.

**Deterministic time**: every data-plane timestamp (creationTimestamp, pod start/conditions, managedFields) is sourced from an injectable clock (`APIServer.SetClock`); a `config.FakeClock` makes them fully deterministic for tests.

**Watch streaming**: each list endpoint accepts `?watch=true` and upgrades to a `Transfer-Encoding: chunked` JSON event stream (`{"type":"ADDED|MODIFIED|DELETED","object":{...}}`). Initial state replays as ADDED events on subscribe, and the request's `labelSelector`/`fieldSelector` filters both the initial snapshot and live events, so `client-go` `Informer` / `SharedIndexInformer` machinery (operator-sdk, Helm, ArgoCD, …) — including selective informers — just works. A fresh cluster bootstraps a synthetic Ready node (`cloudemu-node-0`), and each selector Service's endpoints are mirrored into a `discovery.k8s.io` **EndpointSlice** so EndpointSlice-mode consumers see the same backends as the `Endpoints` object.

**Cascade**: deleting a Namespace or an owning controller publishes DELETED events for every child resource (garbage collection follows `ownerReferences`) — finalizer-bearing children instead go Terminating (MODIFIED) until drained.

**Emulation boundaries** (deliberate simplifications, not gaps): there is no real kubelet — Pods are driven Running synthetically and `pods/log` is synthetic while `exec`/`attach`/`portforward` return a typed 501; no real scheduling beyond the single synthetic node (DaemonSet `nodeSelector` is honored, but affinity/taints/resource-fit are not); admission webhooks make outbound calls only when explicitly enabled (off by default to stay zero-network); server-side apply tracks ownership at leaf granularity (no per-element list merge); NetworkPolicy and RBAC are **queryable** (SubjectAccessReview / EvaluateNetworkPolicy) rather than request-time-enforced, since the emulator has no packet path or authenticated identity; CronJob has no wall-clock timer (schedules are evaluated only when `TickCronJobs` is called) and supports only the standard 5-field cron syntax (nonstandard `@`-macros, `L`/`W`/`#`/`?` characters, and seconds/year fields are rejected); rollouts converge instantly (no surge/unavailable pacing, minimal revision history); and OpenAPI is served cluster-independently, so CRD schemas aren't published there (custom resources still work via discovery).

---

## 19. Resource Discovery

**Engine:** `services/resourcediscovery/` — a cross-service inventory engine that walks the Compute, Networking, Storage, Database, Serverless, Databricks, Kubernetes, and Relational Database drivers of any provider and returns a normalized `Resource` view (provider, service, type, ID, ARN/URN, region, tags, created-at). Auto-wired by every provider factory and exposed as `Provider.ResourceDiscovery`.

**SDK-compat handlers:** AWS Resource Explorer Two + Resource Groups Tagging API, Azure Resource Graph, and GCP Cloud Asset Inventory. All three sit on top of the same engine, so a tag written through any one path is visible through the others.

**Surfaced resource types** (portable `service/Type` → per-provider inventory string):

| Portable | AWS RE2 | Azure Resource Graph | GCP Cloud Asset |
|---|---|---|---|
| `compute/Instance` | `compute:instance` | `microsoft.compute/virtualmachines` | `compute.googleapis.com/Instance` |
| `networking/VPC` · `Subnet` · `SecurityGroup` · `NetworkInterface` · `ElasticIP` | `networking:*` | `microsoft.network/*` | `compute.googleapis.com/*` |
| `storage/Bucket` | `storage:bucket` | `microsoft.storage/storageaccounts` | `storage.googleapis.com/Bucket` |
| `database/Table` | `database:table` | `microsoft.documentdb/databaseaccounts` | `firestore.googleapis.com/Database` |
| `serverless/Function` | `serverless:function` | `microsoft.web/sites` | `cloudfunctions.googleapis.com/Function` |
| `databricks/Workspace` | — | `microsoft.databricks/workspaces` | — |
| `kubernetes/Cluster` | `kubernetes:cluster` | `microsoft.containerservice/managedclusters` | `container.googleapis.com/Cluster` |
| `kubernetes/NodeGroup` | `kubernetes:nodegroup` | `microsoft.containerservice/managedclusters/agentpools` | `container.googleapis.com/NodePool` |

Kubernetes clusters (EKS/GKE/AKS) and their node groups (nodegroups / node pools / agent pools) are surfaced via a `KubernetesClusters` discovery adapter each provider wires in over its cluster mock.

Relational databases follow the same pattern via a `RelationalDatabases` adapter: AWS RDS/Aurora instances, clusters, and snapshots surface through **Resource Explorer 2** (filter `service:rds`) via the `rdsDiscovery` adapter. GCP Cloud SQL and Azure SQL discovery are not yet wired.

### Engine (`services/resourcediscovery/`)

| Operation | Signature |
|-----------|-----------|
| `New` | `(provider, accountID, region string, drivers *Drivers) *Engine` |
| `ListAll` | `(ctx) ([]Resource, error)` |
| `List` | `(ctx, Query) ([]Resource, error)` — filter by `Services`, `Type`, `Region`, `Tags` |
| `SearchByTag` | `(ctx, key, value string) ([]Resource, error)` |
| `GetTagKeys` | `(ctx) ([]string, error)` |
| `GetTagValues` | `(ctx, key string) ([]string, error)` |
| `TagResourceByARN` | `(ctx, arn string, tags map[string]string) error` |
| `UntagResourceByARN` | `(ctx, arn string, keys []string) error` |

### AWS — Resource Explorer 2 (`server/aws/resourceexplorer2`)

| Operation | Notes |
|-----------|-------|
| `Search` | Free-text + filter expression over the unified inventory; returns ARN, resource type, region, owning account, tags |

### AWS — Resource Groups Tagging API (`server/aws/resourcegroupstaggingapi`)

| Operation | Notes |
|-----------|-------|
| `GetResources` | Filter by `ResourceTypeFilters` and `TagFilters`; pagination via `PaginationToken` |
| `TagResources` | Apply a tag set to one or more ARNs in a single call |
| `UntagResources` | Remove tag keys from one or more ARNs in a single call |
| `GetTagKeys` | All tag keys across the inventory |
| `GetTagValues` | All values for a given tag key |

### Azure — Resource Graph (`server/azure/resourcegraph`)

| Operation | Notes |
|-----------|-------|
| `Resources` | `POST /providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01` — KQL-shaped query over the unified inventory; supports `subscriptions[]` scoping and `$top`/`$skipToken` pagination |

**Cost-discovery field projection.** Each row projects the `sku` (name/tier/capacity)
and `properties` a real discoverer prices on, per Azure type:

| Azure type | Projected fields |
|---|---|
| `microsoft.compute/virtualmachines` | `properties.priority` (Spot), `properties.licenseType`, `properties.storageProfile.osDisk.osType`, `sku.name`, `zones` |
| `microsoft.compute/disks` | `properties.diskIOPSReadWrite`, `properties.diskMBpsReadWrite`, `properties.diskSizeGB`, `properties.tier`, `sku.name`/`sku.tier` |
| `microsoft.compute/virtualmachinescalesets` | `sku.name`/`sku.capacity`, nested `properties.virtualMachineProfile.{priority,licenseType,storageProfile.osDisk.osType}` |
| `microsoft.network/publicipaddresses` | `sku.name` (Basic/Standard), `properties.publicIPAllocationMethod` |
| `microsoft.network/virtualnetworks` / `subnets` | `properties.addressSpace.addressPrefixes` / `properties.addressPrefix` |
| `microsoft.sql/managedinstances` | `sku.name`, `properties.vCores`, `properties.tier`, `properties.licenseType`, `properties.storageSizeInGB`, `properties.storageAccountType` (backup redundancy) |
| `microsoft.sql/servers` | `properties.version` (engine version of the logical server) |
| `microsoft.sql/servers/databases` | `sku.name`, `properties.currentSku`, `properties.zoneRedundant` |
| `microsoft.dbformysql`/`dbforpostgresql` `flexibleservers` | `sku.name`/`sku.tier` (derived), `properties.version`, nested `properties.storage.storageSizeGB` + `properties.highAvailability.mode` |
| `microsoft.containerservice/managedclusters` | `sku.tier`, `properties.powerState.code`, `properties.kubernetesVersion` |
| `.../managedclusters/agentpools` | `sku.name` (vmSize), `properties.scaleSetPriority` (Spot), `properties.count`, `properties.mode`/`osType` |
| `microsoft.databricks/workspaces` | `sku.name`/`sku.tier`, `properties.workspaceId`/`provisioningState` |
| `microsoft.storage/storageaccounts` | `sku.name` (redundancy), `kind`, `properties.accessTier` |
| `microsoft.documentdb/databaseaccounts` | `kind`, `properties.databaseAccountOfferType`, `properties.capabilities` (serverless), `properties.enableFreeTier` |
| `microsoft.web/serverfarms` (App Service plan) | `sku.name`/`sku.tier`/`sku.capacity` (pricing tier), `kind` |

Fields are seeded through the portable driver configs (`VolumeConfig.IOPS/Throughput/Tier`,
`InstanceConfig.OSType/Priority/LicenseType/Zones`, `ManagedInstanceConfig.StorageAccountType`,
`ElasticIPConfig.SKU/AllocationMethod`, the AKS `Tier`/`ScaleSetPriority` inputs, the
`Databases` capability on Azure SQL, the VMSS `ScaleSets` + serverfarms `AppServicePlans`
discovery capabilities, and the optional `BucketAttributes` / `TableAttributes` capabilities
that enrich storage accounts and Cosmos DB) so a value set at create time round-trips over
the real `armresourcegraph` SDK. Storage/Cosmos/serverfarms follow the established discovery
patterns — optional type-asserted capabilities (like networking's `NetworkInterfaces`) for
per-resource enrichment, and provider-projected discovery adapters (like the relational-DB
and Kubernetes walkers) for the net-new plan/scale-set resources.

### GCP — Cloud Asset Inventory (`server/gcp/cloudasset`)

| Resource | Operations |
|----------|-----------|
| Assets | `assets.list` (filter by `assetTypes[]`), `searchAllResources` (query string + asset-type filter), `searchAllIamPolicies` (returns empty — out of scope) |
| Export | `exportAssets` — synchronous, returns an `Operation` with inline results |
| Feeds | `feeds.create`, `feeds.list`, `feeds.get`, `feeds.patch`, `feeds.delete` |
| Operations | `operations.get` — fetches cached `exportAssets` results |
| Batch | `batchGetAssetsHistory` |

Operations: **Engine 8** + **AWS Resource Explorer 1** + **AWS Resource Groups Tagging 5** + **Azure Resource Graph 1** + **GCP Cloud Asset 11** = **26**

---

## 20. Generative AI

**Driver interface:** `services/bedrock/driver/driver.go`
**AWS:** `bedrock` (+ `bedrock-runtime`) | **Azure:** — | **GCP:** —

AWS-only. Backs the real `aws-sdk-go-v2/service/bedrock` and `.../bedrockruntime` clients against the in-memory backend.

### Foundation Model Operations

| Operation | Signature |
|-----------|-----------|
| `ListFoundationModels` | `(ctx) ([]FoundationModel, error)` |
| `GetFoundationModel` | `(ctx, modelID) (*FoundationModel, error)` |

### Model Customization Operations

| Operation | Signature |
|-----------|-----------|
| `CreateModelCustomizationJob` | `(ctx, CustomizationJobConfig) (*CustomizationJob, error)` |
| `GetModelCustomizationJob` | `(ctx, jobIdentifier) (*CustomizationJob, error)` |
| `ListModelCustomizationJobs` | `(ctx) ([]CustomizationJob, error)` |

### Custom Model Operations

| Operation | Signature |
|-----------|-----------|
| `ListCustomModels` | `(ctx) ([]CustomModel, error)` |
| `GetCustomModel` | `(ctx, modelIdentifier) (*CustomModel, error)` |
| `DeleteCustomModel` | `(ctx, modelIdentifier) error` |

### Runtime Operations

| Operation | Signature |
|-----------|-----------|
| `InvokeModel` | `(ctx, InvokeModelInput) (*InvokeModelResult, error)` |
| `Converse` | `(ctx, ConverseInput) (*ConverseOutput, error)` |

### Guardrail Operations

| Operation | Signature |
|-----------|-----------|
| `CreateGuardrail` | `(ctx, GuardrailConfig) (*Guardrail, error)` |
| `GetGuardrail` | `(ctx, identifier, version) (*Guardrail, error)` |
| `ListGuardrails` | `(ctx) ([]Guardrail, error)` |
| `UpdateGuardrail` | `(ctx, identifier, GuardrailConfig) (*Guardrail, error)` |
| `DeleteGuardrail` | `(ctx, identifier) error` |

### Provisioned Throughput Operations

| Operation | Signature |
|-----------|-----------|
| `CreateProvisionedModelThroughput` | `(ctx, ProvisionedThroughputConfig) (*ProvisionedThroughput, error)` |
| `GetProvisionedModelThroughput` | `(ctx, identifier) (*ProvisionedThroughput, error)` |
| `ListProvisionedModelThroughputs` | `(ctx) ([]ProvisionedThroughput, error)` |
| `DeleteProvisionedModelThroughput` | `(ctx, identifier) error` |

### Invocation Logging Operations

| Operation | Signature |
|-----------|-----------|
| `PutModelInvocationLoggingConfiguration` | `(ctx, LoggingConfig) error` |
| `GetModelInvocationLoggingConfiguration` | `(ctx) (*LoggingConfig, error)` |
| `DeleteModelInvocationLoggingConfiguration` | `(ctx) error` |

**Total: 22 operations**

---

## 21. Databricks

**Driver interfaces:** `services/databricks/driver/driver.go` (control plane), `services/databricks/driver/dataplane.go` (data plane)
**AWS:** — | **Azure:** `databricks` | **GCP:** —

Azure-only. The control plane backs the real `armdatabricks` SDK; the data plane backs the real `databricks-sdk-go` WorkspaceClient. The SDK-compat-only workspace families (secrets, tokens, git credentials, repos, DBFS, workspace files, SQL warehouses, pipelines, serving endpoints, SCIM identity, Unity Catalog) have no portable Go API — see [sdk-server.md](sdk-server.md).

### Workspace Operations (control plane)

| Operation | Signature |
|-----------|-----------|
| `CreateWorkspace` | `(ctx, WorkspaceConfig) (*Workspace, error)` |
| `GetWorkspace` | `(ctx, resourceGroup, name) (*Workspace, error)` |
| `DeleteWorkspace` | `(ctx, resourceGroup, name) error` |
| `UpdateWorkspaceTags` | `(ctx, resourceGroup, name, tags) (*Workspace, error)` |
| `ListWorkspacesByResourceGroup` | `(ctx, resourceGroup) ([]Workspace, error)` |
| `ListWorkspaces` | `(ctx) ([]Workspace, error)` |

### Extended ARM resources (control plane)

The rest of the `Microsoft.Databricks` ARM surface beyond workspaces
(`services/databricks/driver/arm_resources.go`), reachable over the real
`armdatabricks` SDK:

| Resource | Operations |
|----------|------------|
| **Access Connectors** (`accessConnectors`) | `CreateOrUpdateAccessConnector`, `GetAccessConnector`, `UpdateAccessConnector`, `DeleteAccessConnector`, `ListAccessConnectorsByResourceGroup`, `ListAccessConnectors` |
| **Private Endpoint Connections** (`workspaces/{w}/privateEndpointConnections`) | `PutPrivateEndpointConnection`, `GetPrivateEndpointConnection`, `DeletePrivateEndpointConnection`, `ListPrivateEndpointConnections` |
| **Private Link Resources** (`workspaces/{w}/privateLinkResources`) | `GetPrivateLinkResource`, `ListPrivateLinkResources` |
| **VNet Peering** (`workspaces/{w}/virtualNetworkPeerings`) | `CreateOrUpdateVNetPeering`, `GetVNetPeering`, `DeleteVNetPeering`, `ListVNetPeerings` |
| **Outbound Network Dependencies** (`workspaces/{w}/outboundNetworkDependenciesEndpoints`) | `ListOutboundNetworkDependencies` |
| **Operations** (`/providers/Microsoft.Databricks/operations`) | `ListOperations` |

*Modeled store-and-echo:* the ARM resources round-trip faithfully over the SDK
(access connectors and peerings persist and are listed/described; a
system-assigned access-connector identity gets synthesized principal/tenant
IDs; a created peering springs to `Connected`/`Succeeded`), but the underlying
Azure networking side effects are **not** simulated — a private-endpoint
connection stores its approval state without a real private endpoint on the
platform side, private-link resources and outbound-dependency endpoints are a
synthesized (workspace-scoped) catalog rather than a live probe, and a VNet
peering does not actually peer networks. The provider operations list is a
static catalog of the RBAC operations the namespace exposes.

### Instance Pool Operations

| Operation | Signature |
|-----------|-----------|
| `CreateInstancePool` | `(ctx, InstancePoolConfig) (*InstancePool, error)` |
| `GetInstancePool` | `(ctx, id) (*InstancePool, error)` |
| `ListInstancePools` | `(ctx) ([]InstancePool, error)` |
| `EditInstancePool` | `(ctx, id, InstancePoolConfig) error` |
| `DeleteInstancePool` | `(ctx, id) error` |

### Cluster Operations

| Operation | Signature |
|-----------|-----------|
| `CreateCluster` | `(ctx, ClusterConfig) (*Cluster, error)` |
| `GetCluster` | `(ctx, id) (*Cluster, error)` |
| `ListClusters` | `(ctx) ([]Cluster, error)` |
| `EditCluster` | `(ctx, id, ClusterConfig) error` |
| `DeleteCluster` | `(ctx, id) error` |
| `PermanentDeleteCluster` | `(ctx, id) error` |
| `StartCluster` | `(ctx, id) error` |
| `RestartCluster` | `(ctx, id) error` |
| `ResizeCluster` | `(ctx, id, numWorkers, autoscaleMin, autoscaleMax) error` |
| `PinCluster` | `(ctx, id) error` |
| `UnpinCluster` | `(ctx, id) error` |
| `ListNodeTypes` | `(ctx) ([]NodeType, error)` |
| `ListSparkVersions` | `(ctx) ([]SparkVersion, error)` |
| `ListZones` | `(ctx) (zones, defaultZone, error)` |

### Job Operations

| Operation | Signature |
|-----------|-----------|
| `CreateJob` | `(ctx, JobConfig) (int64, error)` |
| `GetJob` | `(ctx, id) (*Job, error)` |
| `ListJobs` | `(ctx) ([]Job, error)` |
| `UpdateJob` | `(ctx, id, JobConfig) error` |
| `ResetJob` | `(ctx, id, JobConfig) error` |
| `DeleteJob` | `(ctx, id) error` |
| `RunJobNow` | `(ctx, id) (int64, error)` |

### Run Operations

| Operation | Signature |
|-----------|-----------|
| `SubmitRun` | `(ctx, runName) (int64, error)` |
| `GetRun` | `(ctx, runID) (*Run, error)` |
| `ListRuns` | `(ctx, jobID) ([]Run, error)` |
| `CancelRun` | `(ctx, runID) error` |
| `CancelAllRuns` | `(ctx, jobID) error` |
| `DeleteRun` | `(ctx, runID) error` |
| `RepairRun` | `(ctx, runID) (int64, error)` |
| `GetRunOutput` | `(ctx, runID) (*RunOutput, error)` |

### Cluster Policy Operations

| Operation | Signature |
|-----------|-----------|
| `CreateClusterPolicy` | `(ctx, ClusterPolicyConfig) (*ClusterPolicy, error)` |
| `GetClusterPolicy` | `(ctx, policyID) (*ClusterPolicy, error)` |
| `EditClusterPolicy` | `(ctx, policyID, ClusterPolicyConfig) error` |
| `DeleteClusterPolicy` | `(ctx, policyID) error` |
| `ListClusterPolicies` | `(ctx) ([]ClusterPolicy, error)` |

### Library Operations

| Operation | Signature |
|-----------|-----------|
| `InstallLibraries` | `(ctx, clusterID, []LibrarySpec) error` |
| `UninstallLibraries` | `(ctx, clusterID, []LibrarySpec) error` |
| `ClusterLibraryStatuses` | `(ctx, clusterID) ([]LibraryStatus, error)` |
| `AllClusterLibraryStatuses` | `(ctx) ([]ClusterLibraryStatuses, error)` |

### Permission Operations

| Operation | Signature |
|-----------|-----------|
| `GetPermissions` | `(ctx, objectType, objectID) (*ObjectPermissions, error)` |
| `SetPermissions` | `(ctx, objectType, objectID, acl) (*ObjectPermissions, error)` |
| `UpdatePermissions` | `(ctx, objectType, objectID, acl) (*ObjectPermissions, error)` |

**Total: 70 operations**

---

## 22. Machine Learning

### AWS — SageMaker AI

**Driver interface:** `services/sagemaker/driver/driver.go` (control plane + `Runtime`)

The control plane speaks awsJson1_1 (`X-Amz-Target: SageMaker.*`); the runtime speaks
restJson1 (`POST /endpoints/{name}/invocations`). Asynchronous jobs complete synchronously
to a terminal state so Describe/List are deterministic. Auto-metrics → CloudWatch via
`SetMonitoring`.

| Family | Resources / Operations |
|--------|------------------------|
| Jobs | Training, Processing, Transform, HyperParameterTuning, AutoML (V2), Labeling, Compilation — each Create/Describe/List/Stop |
| Inference | Model, EndpointConfig, Endpoint (+ UpdateEndpoint, UpdateEndpointWeightsAndCapacities), InferenceComponent |
| Runtime | InvokeEndpoint, InvokeEndpointAsync (sagemaker-runtime) |
| Model Registry | ModelPackageGroup, ModelPackage (versioned, approval status) |
| Studio | Domain, UserProfile, Space, App |
| Notebooks | NotebookInstance (+ Start/Stop), NotebookInstanceLifecycleConfig, CodeRepository |
| Clusters | HyperPod Cluster (+ ListClusterNodes / DescribeClusterNode) |
| Feature Store | FeatureGroup + online-store runtime (PutRecord / GetRecord / DeleteRecord) |
| Pipelines | Pipeline (+ executions), Experiment, Trial |
| Tagging | AddTags / ListTags / DeleteTags |

SDK-compat HTTP coverage spans every family above, round-tripped against the real
`aws-sdk-go-v2/service/sagemaker`, `sagemakerruntime` and `sagemakerfeaturestoreruntime`
clients. **Total: 121 operations.**

### GCP — Vertex AI

**Driver interface:** `services/vertexai/driver/` — `aiplatform.googleapis.com`

REST rooted at `/v1/projects/{p}/locations/{l}/...` with the Model Garden `generateContent`
surface at `/v1/publishers/...`. Control-plane mutations return done
`google.longrunning.Operation`s; job-family creates are synchronous (poll the `state`
field). Auto-metrics → Cloud Monitoring via `SetMonitoring`.

| Family | Resources / Operations |
|--------|------------------------|
| Datasets | Create/Get/List/Patch/Delete (+ImportData/ExportData) |
| Model registry | UploadModel, Get/List/Patch/Delete, versions, evaluations |
| Endpoints | Create/Get/List/Delete, DeployModel/UndeployModel, Predict/RawPredict |
| Generative AI | generateContent, countTokens (publishers.models + endpoints), tuning jobs, cached contents |
| Jobs | CustomJob, BatchPredictionJob, HyperparameterTuningJob (synchronous create + cancel) |
| Pipelines | TrainingPipeline, PipelineJob |
| Feature Store | Featurestore (+ EntityType + online read/write), FeatureGroup, Feature, FeatureOnlineStore, FeatureView |
| Vector Search | Index (+upsert/remove datapoints), IndexEndpoint (+deploy/undeploy/findNeighbors) |
| ML Metadata | MetadataStore, Tensorboard, Schedule, NotebookRuntimeTemplate, NotebookRuntime |

The full Go API/driver, in-memory provider, and SDK-compat HTTP server (REST round-tripped)
cover every family above — models (+versions/evaluations), endpoints (+predict), datasets,
custom/batch-prediction/hyperparameter-tuning jobs, training & pipeline jobs, tuning jobs,
cached contents, Feature Store (featurestores/entityTypes/features + online read/write),
Feature Registry & online stores, Vector Search (indexes + index endpoints), ML metadata,
tensorboards, schedules, notebook runtimes, and `generateContent`/`countTokens`. A portable
Layer-1 wrapper (`vertexai/vertexai.go`), chaos injection (`chaos.WrapVertexAI`), and cost
rates integrate Vertex with the cross-cutting layers like every other service.
**Total: 128 operations** (Go API/driver).

### Azure — Azure AI

**Driver interface:** `services/ai/driver/` — spans both ARM providers plus the data planes.
**Azure:** Azure AI Foundry / AI Studio / Azure OpenAI (`Microsoft.CognitiveServices`) and
Azure Machine Learning (`Microsoft.MachineLearningServices`).

ARM control-plane PUT returns the resource inline with a terminal `provisioningState` so the
SDK LRO poller terminates on the first response. The data plane is host/path-routed
(`*.openai.azure.com/openai/...`, `*.inference.ml.azure.com/score`). Auto-metrics push to
Azure Monitor via `SetMonitoring`.

| Family | Resources / Operations |
|--------|------------------------|
| AI Services accounts | accounts CRUD, list by RG/sub, listKeys, regenerateKey, listModels, listSkus, listUsages |
| Model deployments | accounts/deployments CRUD + list (gpt-4o, embeddings, …) |
| AI Foundry projects | accounts/projects CRUD + list |
| Responsible AI | accounts/raiPolicies CRUD + list |
| Commitment plans | accounts/commitmentPlans CRUD + list |
| Private endpoints | accounts/privateEndpointConnections CRUD + list |
| Azure OpenAI inference | chat/completions, completions, embeddings |
| Agents / Assistants | assistants, threads, messages, runs (CRUD/list) |
| AML workspaces | workspaces (Default/Hub/Project/FeatureStore) CRUD, list by RG/sub |
| AML compute | computes CRUD + list, start/stop/restart (state machine) |
| AML endpoints | online/batchEndpoints CRUD + list, deployments CRUD + list |
| AML jobs | jobs create/get/list/cancel |
| AML assets | models, data, environments, components, featuresets — versioned CRUD + list (container/versions) |
| AML datastores / connections / schedules | CRUD + list |
| AML registries | cross-workspace registries CRUD + list |
| AML scoring | online-endpoint `/score` data plane |

Full Go API/driver, in-memory provider, SDK-compat ARM + data-plane HTTP server, a portable
Layer-1 wrapper (`ai/ai.go`), chaos injection (`chaos.WrapAzureAI`), and cost rates
integrate Azure AI with the cross-cutting layers like every other service.
**Total: 92 operations** (Go API/driver) — 31 CognitiveServices + 46 MachineLearningServices
+ 15 data plane — all exposed over the SDK-compat HTTP server.

---

## 23. AI Search

**Driver interface:** `services/search/driver/` — `Microsoft.Search/searchServices` (ARM control
plane) plus the `{service}.search.windows.net` data plane.
**Azure:** Azure AI Search (the RAG / retrieval backbone). **AWS / GCP:** _not applicable_.

ARM PUT returns the resource inline with a terminal `provisioningState`; the data plane is
host/path-routed (service name from the `{service}.search.windows.net` subdomain). Auto-metrics
push to Azure Monitor via `SetMonitoring`.

| Family | Resources / Operations |
|--------|------------------------|
| Services (control) | searchServices CRUD, list by RG/sub, update; listAdminKeys, regenerateAdminKey, listQueryKeys, createQueryKey, deleteQueryKey |
| Private networking | sharedPrivateLinkResources CRUD+list, privateEndpointConnections CRUD+list |
| Indexes | create-or-update, get, list, delete |
| Documents | index (upload/merge/mergeOrUpload/delete), search (+count), suggest, autocomplete, count, get-by-key |
| Indexers | create-or-update, get, list, delete, run, reset, status |
| Data sources | create-or-update, get, list, delete |
| Skillsets | create-or-update, get, list, delete |
| Synonym maps | create-or-update, get, list, delete |
| Aliases | create-or-update, get, list, delete |
| Service statistics | counts + storage usage |

Full Go API/driver, in-memory provider, SDK-compat ARM + data-plane HTTP server, a portable
Layer-1 wrapper (`search/search.go`), chaos injection (`chaos.WrapAzureSearch`), and
cost rates integrate Azure AI Search with the cross-cutting layers like every other service.
**Total: 53 operations** (Go API/driver) — 19 control plane + 34 data plane.

---

## 24. Container Orchestration

**Driver interface:** `services/ecs/driver/driver.go`
**AWS:** ECS (`AmazonEC2ContainerServiceV20141113.*`, AWS JSON 1.1) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/ecs` clients work against the SDK-compat
server (`awsserver.Drivers{ECS: cloud.ECS}`).

**Scheduling & placement.** Container instances carry CPU/memory capacity;
`RunTask`/services with launch type **EC2** are first-fit **placed** onto an
instance with sufficient remaining capacity (reserving it, releasing on stop) —
no capacity leaves the task in `failures[]` (`AGENT`/`RESOURCE:*`) or, for a
service, `PENDING`. **FARGATE** requires `networkConfiguration.awsvpcConfiguration`
+ an `awsvpc` task-def with cpu+memory, and synthesizes an ENI `attachment` +
`platformVersion` (no capacity pool). `launchType` is validated against the
task-def's `requiresCompatibilities`.

**Services** converge synchronously: `CreateService` actually launches
`desiredCount` tasks (linked via the `service:<name>` group), records a PRIMARY
`deployment` (rolloutState COMPLETED/IN_PROGRESS) and an `event`; `UpdateService`
reconciles tasks and promotes a new deployment (superseded deployments drain and
are dropped, so the list does not grow); DAEMON runs one task per container
instance (and rejects a caller-supplied `desiredCount`). Batch `Describe*` and
`RunTask` return partial success (`failures[]`) rather than erroring; typed
exceptions (`ClusterNotFoundException`, `ServiceNotFoundException`,
`ClusterContains*Exception`, `InvalidParameterException`, `ClientException`) match
the SDK.

*Accepted but not simulated* (stored and round-tripped so SDK calls succeed, but
with no behavioral effect): `capacityProviderStrategy` (placement still falls
through to EC2/Fargate by launch type — no FARGATE_SPOT/ASG providers),
`loadBalancers` / `serviceRegistries` (no target-group registration, health
checks, or Service Connect), and the deployment circuit-breaker / rollback.
Fargate task-level `cpu`/`memory` **is** validated against the supported
configuration table.

**Composes with EC2 (#300).** `RegisterContainerInstance` provisions a backing
**managed EC2 instance** (`Operator.Managed=true`, principal `ecs.amazonaws.com`,
`aws:ec2:managed-launch` tag), so an ECS container instance is discoverable as a
real EC2 instance subject to managed-resource visibility.

| Family | Operations |
|--------|-----------|
| Clusters | CreateCluster, ListClusters, DescribeClusters, DeleteCluster (cascade-guarded), UpdateCluster, UpdateClusterSettings, PutClusterCapacityProviders |
| Task definitions | RegisterTaskDefinition (auto-incrementing revision; the full container/task runtime surface — portMappings, environment, secrets, healthCheck, logConfiguration, mountPoints, ulimits, resourceRequirements, volumes, ephemeralStorage, runtimePlatform, proxyConfiguration, … — is accepted and round-tripped on the task definition, **not** reflected onto launched containers, which carry only name/image/status), ListTaskDefinitions, DescribeTaskDefinition, DeregisterTaskDefinition, ListTaskDefinitionFamilies |
| Tasks | RunTask (EC2 placement / Fargate ENI), StopTask, ListTasks, DescribeTasks, ExecuteCommand |
| Services | CreateService, UpdateService, ListServices, DescribeServices, DeleteService (force) |
| Container instances | RegisterContainerInstance, DeregisterContainerInstance, UpdateContainerInstancesState (DRAINING), ListContainerInstances, DescribeContainerInstances |
| Tagging | TagResource, UntagResource, ListTagsForResource |
| Account & attributes | PutAccountSetting(+Default), ListAccountSettings, DeleteAccountSetting, PutAttributes, DeleteAttributes, ListAttributes |

**Total: 37 operations.**

---

## 25. DNS Resolver

**Driver interface:** `services/route53resolver/driver/driver.go`
**AWS:** Route 53 Resolver (`Route53Resolver.*`, AWS JSON 1.1) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/route53resolver` clients work against the
SDK-compat server (`awsserver.Drivers{Route53Resolver: cloud.Route53Resolver}`).
Full parity: **all 72 SDK operations**, no stubs. Each resource group is stored
in an in-memory `memstore.Store` guarded by a single mutex; reads are
copy-on-write clones. Every group is covered by a real-SDK round-trip test.

Per-VPC configs (Resolver autodefined-reverse, DNSSEC validation, firewall
fail-open) are **lazily materialized** on first Get with their AWS defaults
(reverse ENABLED, DNSSEC DISABLED, fail-open DISABLED) and only appear in the
corresponding List once touched. Firewall rules are identified within a group by
`(FirewallDomainListId, Qtype)`; deleting a rule group cascades to its rules.

| Family | Operations |
|--------|-----------|
| Resolver endpoints | Create/Get/Update/Delete/ListResolverEndpoint(s), Associate/DisassociateResolverEndpointIpAddress, ListResolverEndpointIpAddresses |
| Resolver rules | Create/Get/Update/Delete/ListResolverRule(s), Associate/DisassociateResolverRule, Get/ListResolverRuleAssociation(s), Put/GetResolverRulePolicy |
| Query-log configs | Create/Get/Delete/ListResolverQueryLogConfig(s), Associate/DisassociateResolverQueryLogConfig, Get/ListResolverQueryLogConfigAssociation(s), Put/GetResolverQueryLogConfigPolicy |
| Resolver & DNSSEC configs | Get/Update/ListResolverConfig(s), Get/Update/ListResolverDnssecConfig(s) |
| DNS Firewall — domain lists | Create/Get/Delete/ListFirewallDomainList(s), Update/Import/ListFirewallDomains |
| DNS Firewall — rules | Create/Update/Delete/ListFirewallRule(s), BatchCreate/BatchUpdate/BatchDeleteFirewallRule |
| DNS Firewall — rule groups | Create/Get/Delete/ListFirewallRuleGroup(s), Put/GetFirewallRuleGroupPolicy |
| DNS Firewall — associations | Associate/Disassociate/Get/Update/ListFirewallRuleGroupAssociation(s) |
| DNS Firewall — configs | Get/Update/ListFirewallConfig(s), ListFirewallRuleTypes |
| Outpost resolvers | Create/Get/Update/Delete/ListOutpostResolver(s) |
| Tagging | TagResource, UntagResource, ListTagsForResource |

*Accepted but not simulated* (stored/echoed so SDK calls succeed, no behavioral
effect): endpoint/rule/config status stays terminal (no async CREATING→OPERATIONAL
transitions); `ImportFirewallDomains` records the request without fetching the S3
file; `ListFirewallRuleTypes` returns an empty descriptor list; resource-share
policies are stored verbatim without RAM enforcement.

**Total: 72 operations.**

---

## 26. Application Networking

**Driver interface:** `services/vpclattice/driver/driver.go`
**AWS:** VPC Lattice (REST-JSON, `awsRestjson1`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/vpclattice` clients work against the
SDK-compat server (`awsserver.Drivers{VPCLattice: cloud.VPCLattice}`). Full
parity: **all 73 SDK operations**, no stubs.

Unlike the AWS JSON 1.1 services, VPC Lattice uses **REST-JSON**: the operation
is selected by HTTP method + URL path (e.g. `POST /services`, `GET
/services/{id}/listeners/{id}`, `PATCH /servicenetworks/{id}`) rather than an
`X-Amz-Target` header. The handler gates on path root + method + **identifier
shape**, so a path-style S3 object op on a bucket named like a Lattice root
(e.g. `GET /services/mykey`) falls through to the S3 catch-all — only a
Lattice-shaped id (a known prefix or a `vpc-lattice` ARN) is claimed. The single
unavoidable residual is a bare `GET /<root>` (list) vs. an S3 list-bucket on an
identically-named bucket. Identifiers accept either a bare ID or a full ARN. Union-typed fields
(a listener's `defaultAction`, a rule's `match`/`action`, a target group's
`config`, a resource configuration's `resourceConfigurationDefinition`) are
stored as raw JSON and echoed back verbatim. Create-time tags are persisted;
deletes block on live service-network associations and cascade contained
children (service→listeners→rules); association counts are recomputed on read
and skip targets that no longer exist.

| Family | Operations |
|--------|-----------|
| Service networks | Create/Get/Update/Delete/ListServiceNetwork(s) |
| Services | Create/Get/Update/Delete/ListService(s) |
| Listeners | Create/Get/Update/Delete/ListListener(s) |
| Rules | Create/Get/Update/Delete/ListRule(s), BatchUpdateRule |
| Target groups & targets | Create/Get/Update/Delete/ListTargetGroup(s), Register/Deregister/ListTargets |
| Service-network associations | Create/Get/Update/Delete/List ServiceNetworkVpcAssociation(s) + ListServiceNetworkVpcEndpointAssociations; Create/Get/Delete/List ServiceNetworkService & ServiceNetworkResource Association(s) |
| Resource configurations | Create/Get/Update/Delete/ListResourceConfiguration(s) |
| Resource gateways | Create/Get/Update/Delete/ListResourceGateway(s) |
| Resource endpoint associations | List/DeleteResourceEndpointAssociation(s) |
| Access-log subscriptions | Create/Get/Update/Delete/ListAccessLogSubscription(s) |
| Auth & resource policies | Put/Get/DeleteAuthPolicy, Put/Get/DeleteResourcePolicy |
| Domain verifications | Start/Get/Delete/ListDomainVerification(s) |
| Tagging | TagResource, UntagResource, ListTagsForResource |

*Accepted but not simulated* (stored/echoed so SDK calls succeed, no behavioral
effect): resources are created directly in a terminal `ACTIVE`/`PENDING` status
(no async state machine); VPC-endpoint and resource-endpoint associations are a
managed surface returned as empty lists; `Forward`/health-check targeting is
stored but not used to route real traffic.

**Total: 73 operations.**

---

## 27. Key Management (KMS)

**Driver interface:** `services/kms/driver/`
**AWS:** KMS (AWS JSON 1.1, `X-Amz-Target: TrentService.<Op>`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/kms` clients (and the `aws kms` CLI) work
against the SDK-compat server (`awsserver.Drivers{KMS: cloud.KMS}`). Full parity
across the key lifecycle, aliases, tags, key policies, grants, rotation,
cryptography, imported key material, and multi-region keys.

**Cryptography is real, not stubbed.** Symmetric keys use AES-256-GCM with a
self-describing ciphertext blob (so `Decrypt` needs no key id) and bind the
encryption context as AEAD additional data; RSA keys use RSA-OAEP-SHA-256.
Sign/Verify use RSA (PSS/PKCS1) and ECDSA over the real key material; MAC uses
HMAC. GenerateDataKey/DataKeyPair return usable key material encrypted under the
KMS key, and ImportKeyMaterial unwraps RSAES-OAEP/PKCS1-wrapped material and
installs it as the AES key.

| Family | Operations |
|--------|-----------|
| Key lifecycle | CreateKey, DescribeKey, ListKeys, EnableKey, DisableKey, UpdateKeyDescription, ScheduleKeyDeletion, CancelKeyDeletion |
| Aliases | CreateAlias, UpdateAlias, DeleteAlias, ListAliases |
| Tags | TagResource, UntagResource, ListResourceTags |
| Key policies | GetKeyPolicy, PutKeyPolicy, ListKeyPolicies |
| Grants | CreateGrant, ListGrants, RevokeGrant, RetireGrant, ListRetirableGrants |
| Rotation | EnableKeyRotation, DisableKeyRotation, GetKeyRotationStatus, ListKeyRotations, RotateKeyOnDemand |
| Cryptography | Encrypt, Decrypt, ReEncrypt, GenerateDataKey(+WithoutPlaintext), GenerateDataKeyPair(+WithoutPlaintext), GenerateRandom, Sign, Verify, GenerateMac, VerifyMac |
| Imported material | GetParametersForImport, ImportKeyMaterial, DeleteImportedKeyMaterial |
| Multi-region | ReplicateKey, UpdatePrimaryRegion |

Key references (ID, key ARN, `alias/<name>`, or alias ARN) resolve uniformly on
every operation.

*Grants are record-only:* CreateGrant/ListGrants/Revoke/Retire manage grant
records, but the emulator carries no caller principal on the wire, so a grant's
operation list and encryption-context constraints are **not** enforced against
crypto calls (there is no principal to evaluate them for). Grant management
round-trips faithfully; request-time authorization is out of scope.

*Out of scope:* Custom Key Stores / CloudHSM / external (XKS) key stores — these
are backed by real HSM hardware or third-party stores that can't be emulated
meaningfully. Multi-region replicas are modeled within a single process, so
cross-region replica lookup isn't observable; `ReplicateKey`/`UpdatePrimaryRegion`
return wire-complete metadata but there is no second regional endpoint to query.

**Total: 45 operations.**

---

## 28. File System (EFS)

**Driver interface:** `services/efs/driver/`
**AWS:** EFS (REST-JSON, `awsRestjson1`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/efs` clients (and the `aws efs` CLI) work
against the SDK-compat server (`awsserver.Drivers{EFS: cloud.EFS}`). Full parity
across file systems, mount targets, access points, lifecycle/backup/replication
configuration, and account preferences.

Unlike the AWS JSON 1.1 services, EFS uses **REST-JSON** with path + method
routing under a fixed API-version prefix (`/2015-02-01/...`). The handler gates
on that prefix, so it never shadows the S3 catch-all (no real bucket path begins
with `/2015-02-01/`). Timestamps are emitted as epoch-seconds numbers, matching
the wire.

| Family | Operations |
|--------|-----------|
| File systems | CreateFileSystem, DeleteFileSystem, DescribeFileSystems, UpdateFileSystem |
| File system policy | PutFileSystemPolicy, DescribeFileSystemPolicy, DeleteFileSystemPolicy |
| Mount targets | CreateMountTarget, DeleteMountTarget, DescribeMountTargets, Describe/ModifyMountTargetSecurityGroups |
| Access points | CreateAccessPoint, DeleteAccessPoint, DescribeAccessPoints |
| Lifecycle | PutLifecycleConfiguration, DescribeLifecycleConfiguration |
| Backup | PutBackupPolicy, DescribeBackupPolicy |
| Replication | CreateReplicationConfiguration, DeleteReplicationConfiguration, DescribeReplicationConfigurations |
| Account preferences | PutAccountPreferences, DescribeAccountPreferences |
| Tags | TagResource, UntagResource, ListTagsForResource + legacy CreateTags/DeleteTags/DescribeTags |

Creating a file system is idempotent on the creation token; a file system with
mount targets can't be deleted until they're removed (NumberOfMountTargets
tracks this). Access points and file systems share one tag store, with the
`Name` tag mirrored onto the resource name. Replication assigns a destination
file-system id per destination; cross-region replicas aren't separately
queryable in a single-process emulator.

**Total: 30 operations.**

## 29. Certificate Manager (ACM)

**Driver interface:** `services/acm/driver/`
**AWS:** ACM (AWS JSON 1.1, `X-Amz-Target: CertificateManager.<Op>`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/acm` clients (and the `aws acm` CLI) work
against the SDK-compat server (`awsserver.Drivers{ACM: cloud.ACM}`). Full parity
across the certificate lifecycle, import/export, renewal, revocation, tags, and
account configuration.

**Certificates are real, not stubbed.** `RequestCertificate` generates a genuine
self-signed X.509 certificate (RSA-2048, SHA-256) for the requested domain +
SANs and — since the emulator can't perform real domain validation — auto-issues
it (status `ISSUED`) so it's immediately usable; `GetCertificate` returns
parseable PEM. `ImportCertificate` validates and stores externally-supplied PEM;
`ExportCertificate` returns the cert, chain, and private key; `RenewCertificate`
re-issues fresh material.

| Family | Operations |
|--------|-----------|
| Lifecycle | RequestCertificate, DescribeCertificate, ListCertificates, DeleteCertificate, GetCertificate |
| Import / export | ImportCertificate, ExportCertificate |
| Renewal / revocation | RenewCertificate, RevokeCertificate, ResendValidationEmail |
| Options | UpdateCertificateOptions (certificate-transparency logging) |
| Search | SearchCertificates |
| Tags | AddTagsToCertificate, RemoveTagsFromCertificate, ListTagsForCertificate |
| Account | GetAccountConfiguration, PutAccountConfiguration |

Certificate identifiers are ARNs. DNS-validation requests expose the CNAME
validation record real clients read. Domain validation is auto-completed in the
emulator (no real DNS/email round-trip), which is the local-dev analog of a
validated public certificate.

**Total: 17 operations.**

---

## 31. Web Application Firewall (WAFv2)

**Driver interface:** `services/wafv2/driver/`
**AWS:** WAFv2 (AWS JSON 1.1, `X-Amz-Target: AWSWAF_20190729.<Op>`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/wafv2` clients (and the `aws wafv2` CLI)
work against the SDK-compat server (`awsserver.Drivers{WAFv2: cloud.WAFv2}`).
Full parity across WebACLs, IPSets, RuleGroups and RegexPatternSets, web-ACL /
resource associations, and tags.

**Scope-partitioned namespace with optimistic locking.** Every resource is keyed
by the tuple `(Scope, Id)`, so `REGIONAL` and `CLOUDFRONT` resources never
collide. Each resource carries a `LockToken` that rotates on every mutation;
`Update*` and `Delete*` must present the current token or the backend returns a
`WAFOptimisticLockException`, exactly as real WAF. Rule, statement,
default-action and visibility-config blocks are stored verbatim (as raw JSON), so
`Get*` returns exactly what `Create*`/`Update*` wrote.

| Family | Operations |
|--------|-----------|
| Web ACLs | CreateWebACL, GetWebACL, UpdateWebACL, DeleteWebACL, ListWebACLs |
| IP sets | CreateIPSet, GetIPSet, UpdateIPSet, DeleteIPSet, ListIPSets |
| Rule groups | CreateRuleGroup, GetRuleGroup, UpdateRuleGroup, DeleteRuleGroup, ListRuleGroups |
| Regex pattern sets | CreateRegexPatternSet, GetRegexPatternSet, UpdateRegexPatternSet, DeleteRegexPatternSet, ListRegexPatternSets |
| Associations | AssociateWebACL, DisassociateWebACL, GetWebACLForResource, ListResourcesForWebACL |
| Tags | TagResource, UntagResource, ListTagsForResource |
| Capacity | CheckCapacity |
| Logging config | PutLoggingConfiguration, GetLoggingConfiguration, DeleteLoggingConfiguration, ListLoggingConfigurations |
| Permission policy | PutPermissionPolicy, GetPermissionPolicy, DeletePermissionPolicy |
| API keys | CreateAPIKey, DeleteAPIKey, ListAPIKeys, GetDecryptedAPIKey |
| Managed products / rule groups / sets | DescribeAllManagedProducts, DescribeManagedProductsByVendor, DescribeManagedRuleGroup, ListAvailableManagedRuleGroups, ListAvailableManagedRuleGroupVersions, ListManagedRuleSets, GetManagedRuleSet, PutManagedRuleSetVersions, UpdateManagedRuleSetVersionExpiryDate |
| Mobile SDK | GenerateMobileSdkReleaseUrl, GetMobileSdkRelease, ListMobileSdkReleases |
| Traffic / statistics | GetRateBasedStatementManagedKeys, GetSampledRequests, GetTopPathStatisticsByTraffic, GetRevenueStatistics, GetRevenueStatisticsSummary, GetRevenueStatisticsTimeSeries, ListSettlementRecords |
| Firewall Manager | DeleteFirewallManagerRuleGroups |

Distinct exceptions (`WAFNonexistentItemException`, `WAFDuplicateItemException`,
`WAFOptimisticLockException`, `WAFInvalidParameterException`) surface as their
real typed errors so SDK `errors.As` checks work.

**Stateful additions.** `CheckCapacity` computes a deterministic, self-consistent
WCU estimate from the submitted rules (documented, non-authoritative — the full
WCU cost table is not modeled). Logging configurations are stored and echoed
verbatim keyed by `ResourceArn`; permission policies are stored per rule-group
ARN; API keys are issued as opaque base64 tokens stored per scope with their
token domains.

**Synthesized read-only ops.** Managed-product/rule-group/rule-set catalogs,
mobile-SDK releases, sampled requests, top-path traffic and revenue/settlement
statistics depend on AWS-managed vendor catalogs and live traffic the emulator
does not model. These return plausible empty (or, for `GenerateMobileSdkReleaseUrl`,
synthesized) results so SDK/CLI calls succeed and round-trip; managed rule set
Get/Put/Update report `WAFNonexistentItemException` since no managed rule sets are
hosted, and `DeleteFirewallManagerRuleGroups` echoes back the presented lock token.

**Total: 59 operations.**
## 30. Email Service (SES v2)

**Driver interface:** `services/sesv2/driver/`
**AWS:** SES v2 (REST-JSON `awsRestjson1`, path prefix `/v2/email/…`) | **Azure:** — | **GCP:** —

AWS-only. Real `aws-sdk-go-v2/service/sesv2` clients (and the `aws sesv2` CLI)
work against the SDK-compat server (`awsserver.Drivers{SESV2: cloud.SESV2}`).
SES v2 uses REST-JSON path + method routing under the `/v2/email/` version
prefix, so its handler gates on that prefix ahead of the S3 catch-all.

**Identities auto-verify.** `CreateEmailIdentity` marks an address or domain
verified for sending immediately (status `SUCCESS`) — the emulator can't perform
a real DNS/email round-trip — and domains receive three Easy-DKIM CNAME tokens.
`SendEmail` validates the from-identity (the address itself or its domain must be
a verified identity) and any referenced configuration set / template, then
returns a generated `MessageId`; accepted messages are retained so tests can
assert on what was sent. `TestRenderEmailTemplate` substitutes `{{key}}`
placeholders from the JSON template data.

Full `aws-sdk-go-v2/service/sesv2` parity: every client method (except
`Options`) is implemented. Beyond the verified/sending core, the emulator also
covers contact lists and contacts, custom verification email templates,
configuration-set event destinations and put-options, dedicated IP pools/IPs,
the deliverability dashboard, email-identity policies and DKIM/feedback/config
-set attributes, import/export jobs, insights/metrics/recommendations, tenants
and tenant-resource associations, reputation entities, multi-region endpoints,
and templated bulk send.

**Synthesized read-only data.** Deliverability, reputation, insights, and metric
figures cannot be observed by an emulator with no real mail flow, so those
operations manage opt-in/association state and return plausible, self-consistent
but non-real reports (e.g. empty blacklist entries, zeroed metric series,
HEALTHY reputation until changed). Import/export jobs complete instantly.

| Family | Operations |
|--------|-----------|
| Email identities | CreateEmailIdentity, GetEmailIdentity, DeleteEmailIdentity, ListEmailIdentities, PutEmailIdentityDkimAttributes, PutEmailIdentityMailFromAttributes |
| Email identity policies / attributes | CreateEmailIdentityPolicy, GetEmailIdentityPolicies, UpdateEmailIdentityPolicy, DeleteEmailIdentityPolicy, PutEmailIdentityConfigurationSetAttributes, PutEmailIdentityDkimSigningAttributes, PutEmailIdentityFeedbackAttributes |
| Configuration sets | CreateConfigurationSet, GetConfigurationSet, DeleteConfigurationSet, ListConfigurationSets |
| Config-set event destinations | Create/Update/Delete ConfigurationSetEventDestination, GetConfigurationSetEventDestinations |
| Config-set put-options | PutConfigurationSet{ArchivingOptions, DeliveryOptions, ReputationOptions, SendingOptions, SuppressionOptions, TrackingOptions, VdmOptions} |
| Email templates | CreateEmailTemplate, GetEmailTemplate, UpdateEmailTemplate, DeleteEmailTemplate, ListEmailTemplates, TestRenderEmailTemplate |
| Custom verification templates | Create/Get/Update/Delete/List CustomVerificationEmailTemplate, SendCustomVerificationEmail |
| Contact lists / contacts | Create/Get/Update/Delete/List ContactList; Create/Get/Update/Delete/List Contact |
| Sending | SendEmail, SendBulkEmail |
| Suppression list | PutSuppressedDestination, GetSuppressedDestination, DeleteSuppressedDestination, ListSuppressedDestinations |
| Dedicated IPs / pools | Create/Delete/Get/List DedicatedIpPool; GetDedicatedIp, GetDedicatedIps, PutDedicatedIpInPool, PutDedicatedIpPoolScalingAttributes, PutDedicatedIpWarmupAttributes, PutAccountDedicatedIpWarmupAttributes |
| Deliverability dashboard | Put/GetDeliverabilityDashboardOption(s), Create/Get/List DeliverabilityTestReport, Get/ListDomainDeliverabilityCampaign(s), GetDomainStatisticsReport, GetBlacklistReports |
| Import / export jobs | Create/Get/List ImportJob; Create/Get/List/Cancel ExportJob |
| Insights / metrics | BatchGetMetricData, GetMessageInsights, GetEmailAddressInsights, ListRecommendations |
| Account | GetAccount, PutAccountSendingAttributes, PutAccountSuppressionAttributes, PutAccountDetails, PutAccountVdmAttributes, PutAccountPricingAttributes |
| Tenants | Create/Get/Delete/List Tenant; Create/Delete TenantResourceAssociation, ListTenantResources, ListResourceTenants, PutTenantSuppressionAttributes |
| Reputation entities | GetReputationEntity, ListReputationEntities, UpdateReputationEntityCustomerManagedStatus, UpdateReputationEntityPolicy |
| Multi-region endpoints | Create/Get/Delete/List MultiRegionEndpoint |
| Tags | TagResource, UntagResource, ListTagsForResource |

Resource identifiers are ARNs (`arn:aws:ses:<region>:<account>:identity/…`,
`…:configuration-set/…`, `…:template/…`); the tag operations resolve the ARN to
the referenced identity, configuration set, or template.

**Total: 113 operations.**
---

## Provider-specific resources

Resources below are served for one provider only, because the concept exists in
one cloud and has no counterpart to abstract. They are reached through the same
endpoints as everything else; the difference is that no portable driver
interface covers them.

### GCP — networking

| Resource | Operations |
|---|---|
| Cloud Routers | insert · get · list · patch · delete |
| Addresses (global and regional) | insert · get · list · delete |
| Service Networking connections | list · create · patch · delete |

Cloud NAT is configured by patching a router, and private services access
reserves a global address and opens a connection. A caller building a private
network uses all three, and releases them when the network goes away.

Addresses are keyed by the scope they were reserved in, so a global address and
a regional one sharing a name stay distinct.

### Azure — Resource Manager

| Resource | Operations |
|---|---|
| Resource groups | create · get · list · delete |
| Subscriptions | list |

Every Azure resource lives in a resource group, so one is created before
anything else and deleted last. A group is usable as soon as it exists;
deleting one that is already gone succeeds, since that is the caller's desired
end state and a teardown retry must not fail on its second pass.

The subscriptions list is empty. This emulator has no tenant model, so it
cannot say which subscriptions a credential reaches, and inventing some would
fabricate an authorization boundary that does not exist here.

## Behavior of published AWS resources

Two families exist in every real AWS account without anyone creating them, so
callers reference them directly. Both are materialized on first reference,
matched against the sets AWS actually publishes — an unrecognized name is
rejected, because accepting anything would let a typo through here and fail
only in production.

| Family | Recognized |
|---|---|
| IAM managed policies (`arn:aws:iam::aws:policy/…`) | A catalog of real policy names, pathed ones included |
| SSM parameters (`/aws/service/…/ami-id`) | The published image trees; the id is derived from the parameter name, so it is stable per parameter and distinct across distros |

## Parameter Store — Run Command (optional capability)

| Operation | Signature |
|---|---|
| `SendCommand` | `(ctx, CommandConfig) (commandID string, error)` |
| `GetCommandInvocation` | `(ctx, commandID, instanceID) (*CommandInvocation, error)` |

Discovered by type assertion on the parameter-store driver, like the subnet and
replication group capabilities.

Targets are validated: sending to an instance that does not exist is
`InvalidInstanceId`, which is the most common Run Command failure during
bring-up.

**Nothing executes.** An emulated instance has no guest operating system, so
invocations report success with empty output. This exercises a caller's
send-and-poll orchestration — that it waits for a terminal status and reads the
response code — but not the script. A caller whose bootstrap script is wrong
still sees success.

## Summary

| Service | Operations |
|---------|-----------|
| Storage | 33 |
| Compute | 35 |
| Database | 21 |
| Serverless | 26 |
| Networking | 51 |
| Networking — AWS-specific (Transit Gateway / VPN / DHCP / prefix lists / egress-only IGW / endpoint services / Client VPN / Traffic Mirroring / Network Insights / VPC Block Public Access / IPAM full incl. discovery/BYOASN/BYOIP/resolver/policy + AWS/IPAM metrics) | 162 |
| Network Firewall — AWS | 20 |
| Monitoring | 12 |
| IAM | 35 |
| DNS | 15 |
| Load Balancer | 21 |
| Message Queue | 14 |
| Cache | 16 (+7 optional) |
| MemoryDB — AWS (Redis/Valkey control plane) | 33 (+13 optional) |
| Keyspaces — AWS (Cassandra control plane) | 18 (+1 optional) |
| Managed Cassandra — Azure (Cosmos DB) | 15 |
| Bigtable — GCP (wide-column NoSQL) | 38 |
| Cosmos DB for PostgreSQL — Azure (Citus) | 34 |
| Secrets | 7 |
| Logging | 13 |
| Notification | 8 |
| Container Registry | 14 |
| Event Bus | 15 |
| Relational Database | 21 (+117 optional) |
| Kubernetes — AWS EKS (control plane) | 21 |
| Kubernetes — Azure AKS (control plane) | 18 |
| Kubernetes — GCP GKE (control plane) | 26 |
| Kubernetes — data plane (30 resources, most × 7 verbs incl. Watch, + /scale and /status subresources) | 249 |
| Resource Discovery (engine + AWS + Azure + GCP handlers) | 26 |
| Generative AI — AWS Bedrock (control plane + runtime) | 65 |
| Generative AI — AWS Bedrock Agent (control plane + runtime) | 32 |
| Databricks — Azure (control + data plane) | 70 |
| Machine Learning — AWS SageMaker (control plane + runtime) | 121 |
| Machine Learning — Azure AI (CognitiveServices + MachineLearningServices + data plane) | 92 |
| Machine Learning — GCP Vertex AI (Go API/driver) | 128 |
| AI Search — Azure AI Search (control + data plane) | 53 |
| Container Orchestration — AWS ECS | 37 |
| DNS Resolver — AWS Route 53 Resolver | 72 |
| Application Networking — AWS VPC Lattice | 73 |
| Key Management — AWS KMS | 45 |
| File System — AWS EFS | 30 |
| Certificate Manager — AWS ACM | 17 |
| Email Service — AWS SES v2 | 113 |
| Web Application Firewall — AWS WAFv2 | 59 |
| **Grand Total** | **1971** (+138 optional) |

Optional operations are capabilities a driver may implement but is not required
to; see the sections marked "optional capability". They are counted separately
because a driver without them is still complete.

Provider-specific resources are not counted: no driver interface covers them,
so there are no driver operations to count.
