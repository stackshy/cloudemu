package main

import (
	"context"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu"
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

func main() {
	ctx := context.Background()

	fmt.Println("==========================================")
	fmt.Println("  CloudEmu — Real Cloud Simulation Demo")
	fmt.Println("==========================================")
	fmt.Println()

	// ===================== AWS =====================
	fmt.Println("--- AWS Cloud ---")
	aws := cloudemu.NewAWS()

	// 1. Create VPC + Networking
	vpc, _ := aws.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
		Tags:      map[string]string{"env": "production"},
	})
	fmt.Printf("  Created VPC: %s (CIDR: %s)\n", vpc.ID, vpc.CIDRBlock)

	subnet, _ := aws.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	fmt.Printf("  Created Subnet: %s (AZ: %s)\n", subnet.ID, subnet.AvailabilityZone)

	sg, _ := aws.VPC.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		Name: "web-sg", Description: "Web traffic", VPCID: vpc.ID,
	})
	aws.VPC.AddIngressRule(ctx, sg.ID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0",
	})
	fmt.Printf("  Created SecurityGroup: %s (port 443 open)\n", sg.ID)

	// 2. Launch EC2 Instances
	instances, _ := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-0abcdef1234", InstanceType: "t3.large",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
		Tags: map[string]string{"app": "web-server", "env": "production"},
	}, 3)
	fmt.Printf("  Launched %d EC2 instances:\n", len(instances))
	for _, inst := range instances {
		fmt.Printf("    - %s | Type: %s | State: %s | IP: %s\n",
			inst.ID, inst.InstanceType, inst.State, inst.PrivateIP)
	}

	// 3. List all running instances
	allRunning, _ := aws.EC2.DescribeInstances(ctx, nil, []computedriver.DescribeFilter{
		{Name: "instance-state-name", Values: []string{"running"}},
	})
	fmt.Printf("  Total running instances: %d\n", len(allRunning))

	// 4. Stop one instance
	fmt.Printf("\n  Stopping instance %s...\n", instances[0].ID)
	aws.EC2.StopInstances(ctx, []string{instances[0].ID})
	desc, _ := aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	fmt.Printf("  Instance %s state: %s\n", instances[0].ID, desc[0].State)

	// 5. Modify stopped instance (resize)
	aws.EC2.ModifyInstance(ctx, instances[0].ID, computedriver.ModifyInstanceInput{
		InstanceType: "t3.xlarge",
	})
	fmt.Printf("  Resized %s to t3.xlarge\n", instances[0].ID)

	// 6. Start it back
	aws.EC2.StartInstances(ctx, []string{instances[0].ID})
	desc, _ = aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	fmt.Printf("  Instance %s state: %s | Type: %s\n", instances[0].ID, desc[0].State, desc[0].InstanceType)

	// 7. Create S3 Bucket + Upload Files
	fmt.Println()
	aws.S3.CreateBucket(ctx, "app-deployments")
	aws.S3.PutObject(ctx, "app-deployments", "v1.0/app.jar", []byte("binary-data-here"), "application/java-archive", nil)
	aws.S3.PutObject(ctx, "app-deployments", "v1.0/config.yaml", []byte("db: rds-prod\nport: 8080"), "text/yaml", nil)
	aws.S3.PutObject(ctx, "app-deployments", "v1.1/app.jar", []byte("new-binary"), "application/java-archive", nil)

	buckets, _ := aws.S3.ListBuckets(ctx)
	fmt.Printf("  S3 Buckets: %d\n", len(buckets))

	objects, _ := aws.S3.ListObjects(ctx, "app-deployments", storagedriver.ListOptions{Prefix: "v1.0/"})
	fmt.Printf("  Objects in v1.0/: %d\n", len(objects.Objects))
	for _, obj := range objects.Objects {
		fmt.Printf("    - %s (%d bytes)\n", obj.Key, obj.Size)
	}

	// List with delimiter (folder-like listing)
	folders, _ := aws.S3.ListObjects(ctx, "app-deployments", storagedriver.ListOptions{Delimiter: "/"})
	fmt.Printf("  Folders: %v\n", folders.CommonPrefixes)

	// Get specific object
	config, _ := aws.S3.GetObject(ctx, "app-deployments", "v1.0/config.yaml")
	fmt.Printf("  Config content: %s\n", string(config.Data))

	// 8. DNS Setup
	fmt.Println()
	zone, _ := aws.Route53.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "mycompany.com"})
	fmt.Printf("  Created DNS Zone: %s (%s)\n", zone.Name, zone.ID)

	aws.Route53.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: "api.mycompany.com", Type: "A", TTL: 300,
		Values: []string{instances[0].PrivateIP},
	})
	aws.Route53.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: "web.mycompany.com", Type: "A", TTL: 300,
		Values: []string{instances[1].PrivateIP},
	})

	records, _ := aws.Route53.ListRecords(ctx, zone.ID)
	fmt.Printf("  DNS Records:\n")
	for _, r := range records {
		fmt.Printf("    - %s %s → %v (TTL: %d)\n", r.Name, r.Type, r.Values, r.TTL)
	}

	// 9. Monitoring — Push and Query Metrics
	fmt.Println()
	now := time.Now()
	aws.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "App/Web", MetricName: "CPUUtilization", Value: 45.2, Timestamp: now, Dimensions: map[string]string{"InstanceId": instances[0].ID}},
		{Namespace: "App/Web", MetricName: "CPUUtilization", Value: 72.8, Timestamp: now, Dimensions: map[string]string{"InstanceId": instances[1].ID}},
		{Namespace: "App/Web", MetricName: "CPUUtilization", Value: 31.5, Timestamp: now, Dimensions: map[string]string{"InstanceId": instances[2].ID}},
		{Namespace: "App/Web", MetricName: "RequestCount", Value: 15230, Timestamp: now},
		{Namespace: "App/Web", MetricName: "ErrorRate", Value: 0.02, Timestamp: now},
	})

	metricNames, _ := aws.CloudWatch.ListMetrics(ctx, "App/Web")
	fmt.Printf("  Available Metrics: %v\n", metricNames)

	cpuResult, _ := aws.CloudWatch.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "App/Web", MetricName: "CPUUtilization",
		Dimensions: map[string]string{"InstanceId": instances[1].ID},
		StartTime:  now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		Period: 60, Stat: "Average",
	})
	fmt.Printf("  CPU for %s: %.1f%%\n", instances[1].ID, cpuResult.Values[0])

	reqResult, _ := aws.CloudWatch.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "App/Web", MetricName: "RequestCount",
		StartTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		Period: 60, Stat: "Sum",
	})
	fmt.Printf("  Total Requests: %.0f\n", reqResult.Values[0])

	// 10. Create Alarm
	aws.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "high-cpu-alarm", Namespace: "App/Web", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
		Period: 300, EvaluationPeriods: 2, Stat: "Average",
	})
	alarms, _ := aws.CloudWatch.DescribeAlarms(ctx, nil)
	fmt.Printf("  Alarms: %d (Name: %s, Threshold: %.0f%%)\n",
		len(alarms), alarms[0].Name, alarms[0].Threshold)

	// 11. Terminate all instances
	fmt.Println()
	fmt.Println("  Cleaning up...")
	for _, inst := range instances {
		aws.EC2.TerminateInstances(ctx, []string{inst.ID})
		fmt.Printf("  Terminated: %s\n", inst.ID)
	}

	// Verify terminated
	allNow, _ := aws.EC2.DescribeInstances(ctx, nil, nil)
	terminatedCount := 0
	for _, inst := range allNow {
		if inst.State == "terminated" {
			terminatedCount++
		}
	}
	fmt.Printf("  Terminated instances: %d/%d\n", terminatedCount, len(allNow))

	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("  All done! No real cloud resources used.")
	fmt.Println("  No AWS account needed. Zero cost.")
	fmt.Println("==========================================")
}
