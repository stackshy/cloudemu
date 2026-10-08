package apprunner_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestVpcConnectorNetworkIsCrossChecked(t *testing.T) {
	opts := config.NewOptions()
	m := apprunner.New(opts)
	net := vpc.New(opts)
	m.SetNetworkResolver(net)

	mk := func(cidr string) (vpcID, subnetID string) {
		v, err := net.CreateVPC(bg, netdriver.VPCConfig{CIDRBlock: cidr})
		requireNoError(t, err)

		s, err := net.CreateSubnet(bg, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: strings.Replace(cidr, "/16", "/24", 1), AvailabilityZone: "us-east-1a"})
		requireNoError(t, err)

		return v.ID, s.ID
	}

	vpcA, subA := mk("10.0.0.0/16")
	vpcB, subB := mk("10.1.0.0/16")

	sgA, err := net.CreateSecurityGroup(bg, netdriver.SecurityGroupConfig{Name: "sg-a", VPCID: vpcA})
	requireNoError(t, err)

	sgB, err := net.CreateSecurityGroup(bg, netdriver.SecurityGroupConfig{Name: "sg-b", VPCID: vpcB})
	requireNoError(t, err)

	for name, in := range map[string]driver.CreateVpcConnectorInput{
		"unknown subnet":                {VpcConnectorName: "conn-1", Subnets: []string{"subnet-nope"}},
		"subnets in two VPCs":           {VpcConnectorName: "conn-1", Subnets: []string{subA, subB}},
		"unknown security group":        {VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{"sg-nope"}},
		"security group in another VPC": {VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{sgB.ID}},
		"bad name":                      {VpcConnectorName: "abc", Subnets: []string{subA}},
	} {
		in := in

		if _, err = m.CreateVpcConnector(bg, &in); err == nil {
			t.Fatalf("%s: expected InvalidRequestException", name)
		}

		requireInvalidRequest(t, err)
	}

	conn, err := m.CreateVpcConnector(bg, &driver.CreateVpcConnectorInput{VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{sgA.ID}})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.ResourceStatusActive)

	// Without a resolver the ids are accepted as given.
	plain := newMock()

	_, err = plain.CreateVpcConnector(bg, &driver.CreateVpcConnectorInput{VpcConnectorName: "conn-1", Subnets: []string{"subnet-1"}})
	requireNoError(t, err)
}

func TestServiceLogGroupsAndMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := apprunner.New(opts)
	logs := cloudwatchlogs.New(opts)
	cw := cloudwatch.New(opts)
	m.SetLogSink(logs)
	m.SetMonitoring(cw)

	svc := mustNamed(t, m, "logged-app")
	base := "/aws/apprunner/logged-app/" + svc.ServiceID

	groups, err := logs.ListLogGroups(bg, scope.Scope{})
	requireNoError(t, err)

	names := map[string]bool{}
	for _, g := range groups {
		names[g.Name] = true
	}

	if !names[base+"/service"] || !names[base+"/application"] {
		t.Fatalf("service and application log groups must be created, got %v", names)
	}

	res, err := cw.GetMetricData(bg, mondriver.GetMetricInput{
		Namespace: "AWS/AppRunner", MetricName: "ActiveInstances",
		Dimensions: map[string]string{"ServiceName": "logged-app", "ServiceID": svc.ServiceID},
		StartTime:  clk.Now().Add(-time.Hour), EndTime: clk.Now().Add(time.Hour), Period: 7200, Stat: "Maximum",
	})
	requireNoError(t, err)

	if len(res.Values) == 0 || res.Unit != "Count" {
		t.Fatalf("ActiveInstances must be published (Count), got %+v", res)
	}

	_, err = m.DeleteService(bg, svc.ServiceArn)
	requireNoError(t, err)

	// App Runner does not remove a deleted service's log groups: they stay until
	// retention expires or the customer deletes them.
	groups, _ = logs.ListLogGroups(bg, scope.Scope{})
	kept := 0

	for _, g := range groups {
		if strings.HasPrefix(g.Name, "/aws/apprunner/logged-app/") {
			kept++
		}
	}

	if kept != 2 {
		t.Fatalf("the service and application log groups must outlive the service, found %d", kept)
	}
}

func TestIngressAndDomainStateSurviveSnapshotRestore(t *testing.T) {
	m := newMock()

	res, err := m.CreateService(bg, privateService("snap-private"))
	requireNoError(t, err)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-snap", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-1", VpcEndpointID: "vpce-1"},
	})
	requireNoError(t, err)

	_, err = m.AssociateCustomDomain(bg, res.Service.ServiceArn, "snap.example.com", nil)
	requireNoError(t, err)

	cfg, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "moved-def"})
	requireNoError(t, err)
	_, err = m.UpdateDefaultAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	requireNoError(t, err)

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	r := newMock()
	requireNoError(t, r.Restore(bg, data))

	got, err := r.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, got.IngressVpcConfiguration.VpcID, "vpc-1")

	desc, _, err := r.DescribeCustomDomains(bg, res.Service.ServiceArn, driver.Page{})
	requireNoError(t, err)

	if len(desc.CustomDomains) != 1 || len(desc.VpcDNSTargets) != 1 {
		t.Fatalf("restore lost the domain or ingress connection: %+v", desc)
	}

	next := mustNamed(t, r, "post-restore")
	assertStr(t, next.AutoScalingConfigurationSummary.AutoScalingConfigurationArn, cfg.AutoScalingConfigurationArn)
}

func TestPartialArnDeleteAndInvalidRevisionNotInDriver(t *testing.T) {
	m := newMock()

	cfg, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "delete-me"})
	requireNoError(t, err)

	base := strings.Join(strings.Split(cfg.AutoScalingConfigurationArn, "/")[:2], "/")

	del, err := m.DeleteAutoScalingConfiguration(bg, base, false)
	requireNoError(t, err)
	assertStr(t, del.AutoScalingConfigurationArn, cfg.AutoScalingConfigurationArn)

	_, err = m.DescribeAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	assertException(t, err, driver.ExResourceNotFound)
}
