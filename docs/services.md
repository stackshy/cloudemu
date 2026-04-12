# Provider Resource Reference

This document lists every service and operation available in CloudEmu across all three cloud providers.

## Master Table

| # | Service Category | AWS | Azure | GCP |
|---|-----------------|-----|-------|-----|
| 1 | Storage | `s3` | `blobstorage` | `gcs` |
| 2 | Compute | `ec2` | `virtualmachines` | `gce` |
| 3 | Database | `dynamodb` | `cosmosdb` | `firestore` |
| 4 | Serverless | `lambda` | `functions` | `cloudfunctions` |
| 5 | Networking | `vpc` | `vnet` | `gcpvpc` |
| 6 | Monitoring | `cloudwatch` | `azuremonitor` | `cloudmonitoring` |
| 7 | IAM | `awsiam` | `azureiam` | `gcpiam` |
| 8 | DNS | `route53` | `azuredns` | `clouddns` |
| 9 | Load Balancer | `elb` | `azurelb` | `gcplb` |
| 10 | Message Queue | `sqs` | `servicebus` | `pubsub` |
| 11 | Cache | `elasticache` | `azurecache` | `memorystore` |
| 12 | Secrets | `secretsmanager` | `keyvault` | `secretmanager` |
| 13 | Logging | `cloudwatchlogs` | `loganalytics` | `cloudlogging` |
| 14 | Notification | `sns` | `notificationhubs` | `fcm` |
| 15 | Container Registry | `ecr` | `acr` | `artifactregistry` |
| 16 | Event Bus | `eventbridge` | `eventgrid` | `eventarc` |

---

## 1. Storage

**Driver interface:** `storage/driver/driver.go`
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

**Driver interface:** `compute/driver/driver.go`
**AWS:** EC2 | **Azure:** Virtual Machines | **GCP:** GCE

### Instance Operations

| Operation | Signature |
|-----------|-----------|
| `RunInstances` | `(ctx, config, count) ([]Instance, error)` |
| `StartInstances` | `(ctx, instanceIDs) error` |
| `StopInstances` | `(ctx, instanceIDs) error` |
| `RebootInstances` | `(ctx, instanceIDs) error` |
| `TerminateInstances` | `(ctx, instanceIDs) error` |
| `DescribeInstances` | `(ctx, instanceIDs, filters) ([]Instance, error)` |
| `ModifyInstance` | `(ctx, instanceID, input) error` |

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

**Driver interface:** `database/driver/driver.go`
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

**Driver interface:** `serverless/driver/driver.go`
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

**Driver interface:** `networking/driver/driver.go`
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

### VPC Endpoints

| Operation | Signature |
|-----------|-----------|
| `CreateVPCEndpoint` | `(ctx, config) (*VPCEndpoint, error)` |
| `DeleteVPCEndpoint` | `(ctx, id) error` |
| `DescribeVPCEndpoints` | `(ctx, ids) ([]VPCEndpoint, error)` |
| `ModifyVPCEndpoint` | `(ctx, id, config) (*VPCEndpoint, error)` |

**Total: 47 operations**

---

## 6. Monitoring

**Driver interface:** `monitoring/driver/driver.go`
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

**Driver interface:** `iam/driver/driver.go`
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

**Driver interface:** `dns/driver/driver.go`
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

**Driver interface:** `loadbalancer/driver/driver.go`
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

### Targets

| Operation | Signature |
|-----------|-----------|
| `RegisterTargets` | `(ctx, targetGroupARN, targets) error` |
| `DeregisterTargets` | `(ctx, targetGroupARN, targets) error` |
| `DescribeTargetHealth` | `(ctx, targetGroupARN) ([]TargetHealth, error)` |
| `SetTargetHealth` | `(ctx, targetGroupARN, targetID, state) error` |

**Total: 19 operations**

---

## 10. Message Queue

**Driver interface:** `messagequeue/driver/driver.go`
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

**Driver interface:** `cache/driver/driver.go`
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

**Total: 16 operations**

---

## 12. Secrets

**Driver interface:** `secrets/driver/driver.go`
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

**Driver interface:** `logging/driver/driver.go`
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

**Driver interface:** `notification/driver/driver.go`
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

**Driver interface:** `containerregistry/driver/driver.go`
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

**Driver interface:** `eventbus/driver/driver.go`
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

## Summary

| Service | Operations |
|---------|-----------|
| Storage | 33 |
| Compute | 35 |
| Database | 21 |
| Serverless | 26 |
| Networking | 47 |
| Monitoring | 12 |
| IAM | 35 |
| DNS | 15 |
| Load Balancer | 19 |
| Message Queue | 14 |
| Cache | 16 |
| Secrets | 7 |
| Logging | 13 |
| Notification | 8 |
| Container Registry | 14 |
| Event Bus | 15 |
| **Grand Total** | **330** |
