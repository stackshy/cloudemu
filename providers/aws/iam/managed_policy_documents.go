package iam

// The documents below are the policies real AWS accounts attach when a
// caller uses one of these ARNs. Most are transcribed from the published
// AWS-managed policy content; a few use a documented wildcard pattern
// (`service:Get*`/`List*`/`Describe*` for a read-only policy, `service:*`
// for a full-access one) where the real policy enumerates dozens of
// individual actions that a wildcard already covers for evaluation purposes
// — those are called out in the doc's own comment. Every entry is faithful
// enough that CheckPermission/SimulatePrincipalPolicy make the same
// allow/deny call a real account would for the actions this emulator's
// wire layer actually authorizes.

// docAdministratorAccess is the real AdministratorAccess document: full
// access to every action on every resource.
const docAdministratorAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "*", "Resource": "*"}
	]
}`

// docPowerUserAccess approximates the real PowerUserAccess document: full
// access to every action except IAM and Organizations administration (the
// real policy also carves out a handful of read/service-linked-role IAM
// actions from that exclusion; this keeps the two exclusions coarse rather
// than enumerating them).
const docPowerUserAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "NotAction": ["iam:*", "organizations:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["iam:CreateServiceLinkedRole", "iam:ListRoles", "iam:GetRole"], "Resource": "*"}
	]
}`

// docReadOnlyAccess approximates the real ReadOnlyAccess document, which
// enumerates read-only actions per service. This uses the documented
// Get*/List*/Describe* read-verb wildcard pattern across every service
// rather than an exhaustive per-service action list.
const docReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["*:Get*", "*:List*", "*:Describe*"], "Resource": "*"}
	]
}`

// docEC2FullAccess is AmazonEC2FullAccess.
const docEC2FullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:*", "elasticloadbalancing:*", "cloudwatch:*", "autoscaling:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": "iam:CreateServiceLinkedRole", "Resource": "*"}
	]
}`

// docEC2ReadOnlyAccess is AmazonEC2ReadOnlyAccess.
const docEC2ReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:Describe*", "elasticloadbalancing:Describe*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudwatch:Get*", "cloudwatch:List*", "cloudwatch:Describe*"], "Resource": "*"},
		{"Effect": "Allow", "Action": "autoscaling:Describe*", "Resource": "*"}
	]
}`

// docVPCFullAccess approximates AmazonVPCFullAccess, which in the real
// policy enumerates every individual VPC-related ec2 verb (CreateVpc,
// DeleteSubnet, ModifyVpcAttribute, ...). Grouped by wildcarded noun instead
// of transcribing the full per-verb list.
const docVPCFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ec2:*Vpc*", "ec2:*Subnet*", "ec2:*Gateway*", "ec2:*Route*",
			"ec2:*NetworkAcl*", "ec2:*SecurityGroup*", "ec2:*Address*",
			"ec2:*NetworkInterface*", "ec2:*PeeringConnection*",
			"ec2:Describe*", "ec2:CreateTags", "ec2:DeleteTags"
		], "Resource": "*"}
	]
}`

// docVPCReadOnlyAccess is AmazonVPCReadOnlyAccess.
const docVPCReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:Describe*", "ec2:Get*"], "Resource": "*"}
	]
}`

// docAutoScalingFullAccess approximates AutoScalingFullAccess (the real
// policy also lists specific elasticloadbalancing/cloudwatch/sns read and
// notification actions individually; grouped by service wildcard here).
const docAutoScalingFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["autoscaling:*", "ec2:Describe*", "elasticloadbalancing:Describe*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudwatch:PutMetricAlarm", "cloudwatch:DescribeAlarms", "cloudwatch:DeleteAlarms",
			"sns:CreateTopic", "sns:Subscribe", "sns:ListTopics"], "Resource": "*"}
	]
}`

// docELBFullAccess is ElasticLoadBalancingFullAccess.
const docELBFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["elasticloadbalancing:*", "ec2:Describe*", "iam:CreateServiceLinkedRole"], "Resource": "*"}
	]
}`

// docEKSClusterPolicy approximates AmazonEKSClusterPolicy: the permissions
// the EKS control plane uses to manage the ENIs, load balancers, and DNS
// records it creates on a caller's behalf.
const docEKSClusterPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:Describe*", "ec2:*NetworkInterface*"], "Resource": "*"},
		{"Effect": "Allow", "Action": "elasticloadbalancing:*", "Resource": "*"},
		{"Effect": "Allow", "Action": ["route53:ChangeResourceRecordSets", "route53:ListHostedZones"], "Resource": "*"}
	]
}`

// docEKSWorkerNodePolicy is AmazonEKSWorkerNodePolicy.
const docEKSWorkerNodePolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:Describe*", "ec2:AttachNetworkInterface", "ec2:CreateTags"], "Resource": "*"},
		{"Effect": "Allow", "Action": "autoscaling:Describe*", "Resource": "*"},
		{"Effect": "Allow", "Action": ["ecr:GetAuthorizationToken", "ecr:BatchCheckLayerAvailability",
			"ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"], "Resource": "*"}
	]
}`

// docEKSServicePolicy is AmazonEKSServicePolicy: the same load-balancer/ENI
// surface as the cluster policy, used by the older EKS service role shape.
const docEKSServicePolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ec2:Describe*", "ec2:*NetworkInterface*"], "Resource": "*"},
		{"Effect": "Allow", "Action": "elasticloadbalancing:*", "Resource": "*"}
	]
}`

// docEKSCNIPolicy is AmazonEKS_CNI_Policy: the actions the VPC CNI plugin
// uses to attach pod ENIs and secondary IPs to worker nodes.
const docEKSCNIPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ec2:AssignPrivateIpAddresses", "ec2:AttachNetworkInterface",
			"ec2:CreateNetworkInterface", "ec2:DeleteNetworkInterface",
			"ec2:DescribeInstances", "ec2:DescribeTags", "ec2:DescribeNetworkInterfaces",
			"ec2:DescribeInstanceTypes", "ec2:DetachNetworkInterface",
			"ec2:ModifyNetworkInterfaceAttribute", "ec2:UnassignPrivateIpAddresses"
		], "Resource": "*"},
		{"Effect": "Allow", "Action": "ec2:CreateTags", "Resource": "arn:aws:ec2:*:*:network-interface/*"}
	]
}`

// docEKSVPCResourceController is AmazonEKSVPCResourceController: the
// branch-ENI-for-pods permissions the EKS control plane assumes for
// security-group-per-pod.
const docEKSVPCResourceController = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ec2:DescribeSubnets", "ec2:DescribeNetworkInterfaces", "ec2:DescribeInstances",
			"ec2:AssignPrivateIpAddresses", "ec2:UnassignPrivateIpAddresses",
			"ec2:CreateNetworkInterface", "ec2:AttachNetworkInterface",
			"ec2:DeleteNetworkInterface", "ec2:DetachNetworkInterface"
		], "Resource": "*"}
	]
}`

// docECRReadOnly is AmazonEC2ContainerRegistryReadOnly.
const docECRReadOnly = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ecr:GetAuthorizationToken", "ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer",
			"ecr:GetRepositoryPolicy", "ecr:DescribeRepositories", "ecr:ListImages", "ecr:DescribeImages",
			"ecr:BatchGetImage", "ecr:GetLifecyclePolicy", "ecr:GetLifecyclePolicyPreview",
			"ecr:ListTagsForResource", "ecr:DescribeImageScanFindings"
		], "Resource": "*"}
	]
}`

// docECRPowerUser is AmazonEC2ContainerRegistryPowerUser: read-only plus the
// ability to push images.
const docECRPowerUser = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ecr:GetAuthorizationToken", "ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer",
			"ecr:GetRepositoryPolicy", "ecr:DescribeRepositories", "ecr:ListImages", "ecr:DescribeImages",
			"ecr:BatchGetImage", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
			"ecr:CompleteLayerUpload", "ecr:PutImage"
		], "Resource": "*"}
	]
}`

// docECRFullAccess is AmazonEC2ContainerRegistryFullAccess.
const docECRFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "ecr:*", "Resource": "*"}
	]
}`

// docECSFullAccess approximates AmazonECS_FullAccess (the real policy also
// lists specific elasticloadbalancing/ec2/cloudwatch read actions and a
// scoped iam:PassRole; grouped by service wildcard here).
const docECSFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ecs:*", "elasticloadbalancing:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["ec2:Describe*", "cloudwatch:Get*", "cloudwatch:List*", "cloudwatch:Describe*"],
			"Resource": "*"}
	]
}`

// docECSTaskExecutionRolePolicy is
// service-role/AmazonECSTaskExecutionRolePolicy: the pull-image and
// log/secret-fetch permissions the ECS agent assumes on a task's behalf.
const docECSTaskExecutionRolePolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ecr:GetAuthorizationToken", "ecr:BatchCheckLayerAvailability",
			"ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage",
			"logs:CreateLogStream", "logs:PutLogEvents",
			"secretsmanager:GetSecretValue", "ssm:GetParameters"
		], "Resource": "*"}
	]
}`

// docSSMManagedInstanceCore approximates AmazonSSMManagedInstanceCore: the
// permissions the SSM Agent uses to register an instance and run commands.
const docSSMManagedInstanceCore = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ssm:*", "ssmmessages:*", "ec2messages:*"], "Resource": "*"}
	]
}`

// docSSMFullAccess is AmazonSSMFullAccess.
const docSSMFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "ssm:*", "Resource": "*"}
	]
}`

// docSSMReadOnlyAccess is AmazonSSMReadOnlyAccess.
const docSSMReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ssm:Describe*", "ssm:Get*", "ssm:List*"], "Resource": "*"}
	]
}`

// docEC2RoleforSSM is service-role/AmazonEC2RoleforSSM, the deprecated
// predecessor to AmazonSSMManagedInstanceCore with the same shape of grant.
const docEC2RoleforSSM = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ssm:*", "ec2messages:*", "cloudwatch:PutMetricData", "logs:*"], "Resource": "*"}
	]
}`

// docSSMAutomationRole approximates service-role/AmazonSSMAutomationRole:
// the actions an SSM Automation execution assumes to run its steps.
const docSSMAutomationRole = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["ssm:*", "ec2:Describe*", "lambda:InvokeFunction", "sns:Publish"], "Resource": "*"},
		{"Effect": "Allow", "Action": "iam:PassRole", "Resource": "*"}
	]
}`

// docS3FullAccess is AmazonS3FullAccess.
const docS3FullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["s3:*", "s3-object-lambda:*"], "Resource": "*"}
	]
}`

// docS3ReadOnlyAccess is AmazonS3ReadOnlyAccess.
const docS3ReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["s3:Get*", "s3:List*", "s3-object-lambda:Get*", "s3-object-lambda:List*"],
			"Resource": "*"}
	]
}`

// docRDSFullAccess approximates AmazonRDSFullAccess (the real policy also
// lists specific ec2/sns read actions individually; grouped by service
// wildcard here).
const docRDSFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "rds:*", "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudwatch:Get*", "cloudwatch:List*", "cloudwatch:Describe*",
			"ec2:Describe*", "sns:ListSubscriptions", "sns:ListTopics", "sns:Publish", "logs:*"], "Resource": "*"}
	]
}`

// docRDSReadOnlyAccess is AmazonRDSReadOnlyAccess.
const docRDSReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["rds:Describe*", "rds:List*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudwatch:Get*", "cloudwatch:List*", "cloudwatch:Describe*",
			"ec2:Describe*", "logs:Describe*", "logs:Get*"], "Resource": "*"}
	]
}`

// docDynamoDBFullAccess approximates AmazonDynamoDBFullAccess (the real
// policy also grants dax/application-autoscaling/datapipeline actions
// individually; grouped by service wildcard here).
const docDynamoDBFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["dynamodb:*", "dax:*", "application-autoscaling:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudwatch:Describe*", "cloudwatch:Get*", "cloudwatch:List*",
			"cloudwatch:PutMetricAlarm", "cloudwatch:DeleteAlarms"], "Resource": "*"}
	]
}`

// docDynamoDBReadOnlyAccess is AmazonDynamoDBReadOnlyAccess.
const docDynamoDBReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"dynamodb:BatchGetItem", "dynamodb:Get*", "dynamodb:Describe*", "dynamodb:List*",
			"dynamodb:Query", "dynamodb:Scan", "dax:BatchGetItem", "dax:Get*",
			"dax:Describe*", "dax:List*", "dax:Query", "dax:Scan"
		], "Resource": "*"},
		{"Effect": "Allow", "Action": ["application-autoscaling:Describe*", "cloudwatch:Describe*",
			"cloudwatch:Get*", "cloudwatch:List*"], "Resource": "*"}
	]
}`

// docElastiCacheFullAccess is AmazonElastiCacheFullAccess.
const docElastiCacheFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "elasticache:*", "Resource": "*"},
		{"Effect": "Allow", "Action": ["ec2:DescribeSecurityGroups", "ec2:DescribeSubnets", "ec2:DescribeVpcs",
			"sns:CreateTopic", "sns:ListTopics", "cloudwatch:Describe*", "cloudwatch:Get*", "cloudwatch:List*"],
			"Resource": "*"}
	]
}`

// docEBSCSIDriverPolicy is service-role/AmazonEBSCSIDriverPolicy: the
// volume lifecycle actions the EBS CSI driver assumes.
const docEBSCSIDriverPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": [
			"ec2:CreateSnapshot", "ec2:AttachVolume", "ec2:DetachVolume", "ec2:ModifyVolume",
			"ec2:CreateTags", "ec2:DeleteTags", "ec2:CreateVolume", "ec2:DeleteVolume",
			"ec2:DescribeInstances", "ec2:DescribeSnapshots", "ec2:DescribeTags",
			"ec2:DescribeVolumes", "ec2:DescribeVolumesModifications"
		], "Resource": "*"}
	]
}`

// docSecretsManagerReadWrite approximates SecretsManagerReadWrite (the real
// policy also grants scoped cloudformation and lambda actions used by the
// rotation-function console wizard; grouped by service wildcard here).
//
// service, not a hardcoded credential.
//
//nolint:gosec // this is an IAM policy document naming the secretsmanager
const docSecretsManagerReadWrite = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "secretsmanager:*", "Resource": "*"},
		{"Effect": "Allow", "Action": ["kms:Decrypt", "kms:DescribeKey", "kms:GenerateDataKey", "kms:ListAliases",
			"kms:ListKeys", "lambda:ListFunctions"], "Resource": "*"}
	]
}`

// docLambdaFullAccess is AWSLambda_FullAccess.
const docLambdaFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "lambda:*", "Resource": "*"}
	]
}`

// docLambdaBasicExecutionRole is service-role/AWSLambdaBasicExecutionRole:
// the CloudWatch Logs write access every Lambda execution role needs.
const docLambdaBasicExecutionRole = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"],
			"Resource": "*"}
	]
}`

// docLambdaVPCAccessExecutionRole is
// service-role/AWSLambdaVPCAccessExecutionRole: basic execution plus the
// ENI actions a VPC-attached function needs.
const docLambdaVPCAccessExecutionRole = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"],
			"Resource": "*"},
		{"Effect": "Allow", "Action": [
			"ec2:CreateNetworkInterface", "ec2:DescribeNetworkInterfaces", "ec2:DeleteNetworkInterface",
			"ec2:AssignPrivateIpAddresses", "ec2:UnassignPrivateIpAddresses"
		], "Resource": "*"}
	]
}`

// docCloudWatchAgentServerPolicy is CloudWatchAgentServerPolicy: the metric
// and log push permissions the unified CloudWatch Agent uses on a host.
const docCloudWatchAgentServerPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["cloudwatch:PutMetricData", "ec2:DescribeVolumes", "ec2:DescribeTags"],
			"Resource": "*"},
		{"Effect": "Allow", "Action": ["logs:PutLogEvents", "logs:DescribeLogStreams", "logs:DescribeLogGroups",
			"logs:CreateLogStream", "logs:CreateLogGroup"], "Resource": "*"},
		{"Effect": "Allow", "Action": "ssm:GetParameter", "Resource": "*"}
	]
}`

// docCloudWatchFullAccess approximates CloudWatchFullAccess (the real
// policy also grants scoped oam:* cross-account-observability actions;
// omitted since this emulator has no oam surface).
const docCloudWatchFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["cloudwatch:*", "logs:*", "sns:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": "iam:CreateServiceLinkedRole", "Resource": "*"}
	]
}`

// docCloudWatchLogsFullAccess is CloudWatchLogsFullAccess.
const docCloudWatchLogsFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "logs:*", "Resource": "*"}
	]
}`

// docCloudWatchReadOnlyAccess is CloudWatchReadOnlyAccess.
const docCloudWatchReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["cloudwatch:Describe*", "cloudwatch:Get*", "cloudwatch:List*"],
			"Resource": "*"},
		{"Effect": "Allow", "Action": ["logs:Describe*", "logs:Get*", "logs:List*", "logs:TestMetricFilter",
			"sns:List*"], "Resource": "*"}
	]
}`

// docXRayDaemonWriteAccess is AWSXRayDaemonWriteAccess.
const docXRayDaemonWriteAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["xray:PutTraceSegments", "xray:PutTelemetryRecords",
			"xray:GetSamplingRules", "xray:GetSamplingTargets", "xray:GetSamplingStatisticSummaries"],
			"Resource": "*"}
	]
}`

// docSQSFullAccess is AmazonSQSFullAccess.
const docSQSFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "sqs:*", "Resource": "*"}
	]
}`

// docSNSFullAccess is AmazonSNSFullAccess.
const docSNSFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "sns:*", "Resource": "*"}
	]
}`

// docRoute53FullAccess approximates AmazonRoute53FullAccess.
const docRoute53FullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["route53:*", "route53domains:*"], "Resource": "*"},
		{"Effect": "Allow", "Action": ["cloudfront:ListDistributions", "elasticloadbalancing:DescribeLoadBalancers",
			"s3:GetBucketLocation", "s3:GetBucketWebsite"], "Resource": "*"}
	]
}`

// docIAMFullAccess is IAMFullAccess.
const docIAMFullAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": "iam:*", "Resource": "*"}
	]
}`

// docIAMReadOnlyAccess is IAMReadOnlyAccess.
const docIAMReadOnlyAccess = `{
	"Version": "2012-10-17",
	"Statement": [
		{"Effect": "Allow", "Action": ["iam:Get*", "iam:List*", "iam:GenerateCredentialReport",
			"iam:GenerateServiceLastAccessedDetails", "iam:SimulateCustomPolicy", "iam:SimulatePrincipalPolicy"],
			"Resource": "*"}
	]
}`

// awsManagedPolicyDocuments maps each cataloged AWS-managed policy name (the
// part of the ARN after awsManagedPolicyPrefix, path included) to its policy
// document. This is the same finite, fixed set AWS itself publishes — see
// the package comment on ensureAWSManagedPolicy for why an unlisted name is
// rejected rather than accepted with a placeholder.
//
//nolint:gochecknoglobals // static lookup table
var awsManagedPolicyDocuments = map[string]string{
	// Broad access
	"AdministratorAccess": docAdministratorAccess,
	"PowerUserAccess":     docPowerUserAccess,
	"ReadOnlyAccess":      docReadOnlyAccess,

	// EC2 / VPC / autoscaling
	"AmazonEC2FullAccess":            docEC2FullAccess,
	"AmazonEC2ReadOnlyAccess":        docEC2ReadOnlyAccess,
	"AmazonVPCFullAccess":            docVPCFullAccess,
	"AmazonVPCReadOnlyAccess":        docVPCReadOnlyAccess,
	"AutoScalingFullAccess":          docAutoScalingFullAccess,
	"ElasticLoadBalancingFullAccess": docELBFullAccess,

	// EKS
	"AmazonEKSClusterPolicy":         docEKSClusterPolicy,
	"AmazonEKSWorkerNodePolicy":      docEKSWorkerNodePolicy,
	"AmazonEKSServicePolicy":         docEKSServicePolicy,
	"AmazonEKS_CNI_Policy":           docEKSCNIPolicy,
	"AmazonEKSVPCResourceController": docEKSVPCResourceController,

	// ECR / ECS
	"AmazonEC2ContainerRegistryReadOnly":            docECRReadOnly,
	"AmazonEC2ContainerRegistryPowerUser":           docECRPowerUser,
	"AmazonEC2ContainerRegistryFullAccess":          docECRFullAccess,
	"AmazonECS_FullAccess":                          docECSFullAccess,
	"service-role/AmazonECSTaskExecutionRolePolicy": docECSTaskExecutionRolePolicy,

	// Systems Manager
	"AmazonSSMManagedInstanceCore":         docSSMManagedInstanceCore,
	"AmazonSSMFullAccess":                  docSSMFullAccess,
	"AmazonSSMReadOnlyAccess":              docSSMReadOnlyAccess,
	"service-role/AmazonEC2RoleforSSM":     docEC2RoleforSSM,
	"service-role/AmazonSSMAutomationRole": docSSMAutomationRole,

	// Storage / databases
	"AmazonS3FullAccess":                    docS3FullAccess,
	"AmazonS3ReadOnlyAccess":                docS3ReadOnlyAccess,
	"AmazonRDSFullAccess":                   docRDSFullAccess,
	"AmazonRDSReadOnlyAccess":               docRDSReadOnlyAccess,
	"AmazonDynamoDBFullAccess":              docDynamoDBFullAccess,
	"AmazonDynamoDBReadOnlyAccess":          docDynamoDBReadOnlyAccess,
	"AmazonElastiCacheFullAccess":           docElastiCacheFullAccess,
	"service-role/AmazonEBSCSIDriverPolicy": docEBSCSIDriverPolicy,
	"SecretsManagerReadWrite":               docSecretsManagerReadWrite,

	// Lambda
	"AWSLambda_FullAccess":                         docLambdaFullAccess,
	"service-role/AWSLambdaBasicExecutionRole":     docLambdaBasicExecutionRole,
	"service-role/AWSLambdaVPCAccessExecutionRole": docLambdaVPCAccessExecutionRole,

	// Observability / messaging / DNS
	"CloudWatchAgentServerPolicy": docCloudWatchAgentServerPolicy,
	"CloudWatchFullAccess":        docCloudWatchFullAccess,
	"CloudWatchLogsFullAccess":    docCloudWatchLogsFullAccess,
	"CloudWatchReadOnlyAccess":    docCloudWatchReadOnlyAccess,
	"AWSXRayDaemonWriteAccess":    docXRayDaemonWriteAccess,
	"AmazonSQSFullAccess":         docSQSFullAccess,
	"AmazonSNSFullAccess":         docSNSFullAccess,
	"AmazonRoute53FullAccess":     docRoute53FullAccess,

	// IAM
	"IAMFullAccess":     docIAMFullAccess,
	"IAMReadOnlyAccess": docIAMReadOnlyAccess,
}
