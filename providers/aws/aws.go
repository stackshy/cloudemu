// Package aws provides AWS mock provider factories.
package aws

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/awsiam"
	"github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/ecr"
	"github.com/stackshy/cloudemu/v2/providers/aws/eks"
	"github.com/stackshy/cloudemu/v2/providers/aws/elasticache"
	"github.com/stackshy/cloudemu/v2/providers/aws/elb"
	"github.com/stackshy/cloudemu/v2/providers/aws/eventbridge"
	"github.com/stackshy/cloudemu/v2/providers/aws/lambda"
	"github.com/stackshy/cloudemu/v2/providers/aws/rds"
	"github.com/stackshy/cloudemu/v2/providers/aws/redshift"
	"github.com/stackshy/cloudemu/v2/providers/aws/route53"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/providers/aws/sagemaker"
	"github.com/stackshy/cloudemu/v2/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/v2/providers/aws/sns"
	"github.com/stackshy/cloudemu/v2/providers/aws/sqs"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// eksDiscovery adapts the EKS mock to the resourcediscovery KubernetesClusters
// capability, so EKS clusters and their node groups surface in Resource
// Explorer. Kept in the provider package (not services/) to avoid inverting
// the layering — the discovery engine stays free of provider imports.
type eksDiscovery struct{ m *eks.Mock }

func (a eksDiscovery) DiscoverClusters(ctx context.Context) ([]resourcediscovery.DiscoveredCluster, error) {
	names, err := a.m.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredCluster, 0, len(names))

	for _, name := range names {
		dc := resourcediscovery.DiscoveredCluster{Name: name}

		if c, cerr := a.m.DescribeCluster(ctx, name); cerr == nil && c != nil {
			dc.Tags = c.Tags
		}

		if ngs, ngErr := a.m.ListNodegroups(ctx, name); ngErr == nil {
			dc.NodeGroups = ngs
		}

		out = append(out, dc)
	}

	return out, nil
}

// Provider holds all AWS mock services.
type Provider struct {
	S3                *s3.Mock
	EC2               *ec2.Mock
	DynamoDB          *dynamodb.Mock
	Lambda            *lambda.Mock
	VPC               *vpc.Mock
	CloudWatch        *cloudwatch.Mock
	IAM               *awsiam.Mock
	Route53           *route53.Mock
	ELB               *elb.Mock
	SQS               *sqs.Mock
	ElastiCache       *elasticache.Mock
	SecretsManager    *secretsmanager.Mock
	CloudWatchLogs    *cloudwatchlogs.Mock
	SNS               *sns.Mock
	ECR               *ecr.Mock
	EventBridge       *eventbridge.Mock
	RDS               *rds.Mock
	Redshift          *redshift.Mock
	EKS               *eks.Mock
	Bedrock           *bedrock.Mock
	SageMaker         *sagemaker.Mock
	SSM               *ssm.Mock
	ResourceDiscovery *resourcediscovery.Engine
	AccountID         string
	Region            string
}

// New creates a new AWS provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		S3:             s3.New(o),
		EC2:            ec2.New(o),
		DynamoDB:       dynamodb.New(o),
		Lambda:         lambda.New(o),
		VPC:            vpc.New(o),
		CloudWatch:     cloudwatch.New(o),
		IAM:            awsiam.New(o),
		Route53:        route53.New(o),
		ELB:            elb.New(o),
		SQS:            sqs.New(o),
		ElastiCache:    elasticache.New(o),
		SecretsManager: secretsmanager.New(o),
		CloudWatchLogs: cloudwatchlogs.New(o),
		SNS:            sns.New(o),
		ECR:            ecr.New(o),
		EventBridge:    eventbridge.New(o),
		RDS:            rds.New(o),
		Redshift:       redshift.New(o),
		EKS:            eks.New(o),
		Bedrock:        bedrock.New(o),
		SageMaker:      sagemaker.New(o),
		SSM:            ssm.New(o),
		AccountID:      o.AccountID,
		Region:         o.Region,
	}
	p.EC2.SetMonitoring(p.CloudWatch)
	p.S3.SetMonitoring(p.CloudWatch)
	p.DynamoDB.SetMonitoring(p.CloudWatch)
	p.Lambda.SetMonitoring(p.CloudWatch)
	p.SQS.SetMonitoring(p.CloudWatch)
	p.ElastiCache.SetMonitoring(p.CloudWatch)
	p.CloudWatchLogs.SetMonitoring(p.CloudWatch)
	p.SNS.SetMonitoring(p.CloudWatch)
	p.ECR.SetMonitoring(p.CloudWatch)
	p.EventBridge.SetMonitoring(p.CloudWatch)
	p.RDS.SetMonitoring(p.CloudWatch)
	p.RDS.SetSubnetResolver(p.VPC)
	p.ElastiCache.SetSubnetResolver(p.VPC)
	p.SSM.SetInstanceResolver(p.EC2)
	p.Redshift.SetMonitoring(p.CloudWatch)
	p.EKS.SetMonitoring(p.CloudWatch)
	p.SageMaker.SetMonitoring(p.CloudWatch)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAWS, o.AccountID, o.Region,
		&resourcediscovery.Drivers{
			Compute:    p.EC2,
			Networking: p.VPC,
			Storage:    p.S3,
			Database:   p.DynamoDB,
			Serverless: p.Lambda,
			Kubernetes: eksDiscovery{p.EKS},
		},
	)

	return p
}
