export interface ServiceMapping {
  category: string;
  icon: string;
  aws: string;
  azure: string;
  gcp: string;
  slug: string;
  description: string;
}

export const services: ServiceMapping[] = [
  { category: 'Compute', icon: 'Server', aws: 'EC2', azure: 'VirtualMachines', gcp: 'GCE', slug: 'compute', description: 'Virtual machine instances with lifecycle state machines' },
  { category: 'Storage', icon: 'HardDrive', aws: 'S3', azure: 'BlobStorage', gcp: 'GCS', slug: 'storage', description: 'Object storage with buckets, versioning, and multipart upload' },
  { category: 'Database', icon: 'Database', aws: 'DynamoDB', azure: 'CosmosDB', gcp: 'Firestore', slug: 'database', description: 'NoSQL database with queries, TTL, and streams' },
  { category: 'Serverless', icon: 'Zap', aws: 'Lambda', azure: 'Functions', gcp: 'CloudFunctions', slug: 'serverless', description: 'Function-as-a-service with versions and aliases' },
  { category: 'Networking', icon: 'Network', aws: 'VPC', azure: 'VNet', gcp: 'VPC', slug: 'networking', description: 'Virtual networks, subnets, and security groups' },
  { category: 'Monitoring', icon: 'Activity', aws: 'CloudWatch', azure: 'Monitor', gcp: 'CloudMonitoring', slug: 'monitoring', description: 'Metrics, alarms, and metric queries' },
  { category: 'IAM', icon: 'Shield', aws: 'IAM', azure: 'IAM', gcp: 'IAM', slug: 'iam', description: 'Identity, roles, and policy evaluation' },
  { category: 'DNS', icon: 'Globe', aws: 'Route53', azure: 'DNS', gcp: 'CloudDNS', slug: 'dns', description: 'DNS zones and records with weighted routing' },
  { category: 'Load Balancer', icon: 'GitBranch', aws: 'ELB', azure: 'LB', gcp: 'LB', slug: 'loadbalancer', description: 'Load balancers, target groups, and health checks' },
  { category: 'Message Queue', icon: 'MessageSquare', aws: 'SQS', azure: 'ServiceBus', gcp: 'PubSub', slug: 'messagequeue', description: 'Queues with FIFO dedup and dead-letter queues' },
  { category: 'Notification', icon: 'Bell', aws: 'SNS', azure: 'NotificationHubs', gcp: 'FCM', slug: 'notification', description: 'Topics, subscriptions, and push notifications' },
  { category: 'Event Bus', icon: 'Radio', aws: 'EventBridge', azure: 'EventGrid', gcp: 'Eventarc', slug: 'eventbus', description: 'Event routing with rules and targets' },
  { category: 'Container Registry', icon: 'Box', aws: 'ECR', azure: 'ACR', gcp: 'ArtifactRegistry', slug: 'containerregistry', description: 'Container image storage and lifecycle' },
  { category: 'Cache', icon: 'MemoryStick', aws: 'ElastiCache', azure: 'Cache', gcp: 'Memorystore', slug: 'cache', description: 'In-memory cache with TTL support' },
  { category: 'Secrets', icon: 'Lock', aws: 'SecretsManager', azure: 'KeyVault', gcp: 'SecretManager', slug: 'secrets', description: 'Secret storage and versioning' },
  { category: 'Logging', icon: 'FileText', aws: 'CloudWatchLogs', azure: 'LogAnalytics', gcp: 'CloudLogging', slug: 'logging', description: 'Log groups and log streams' },
];
