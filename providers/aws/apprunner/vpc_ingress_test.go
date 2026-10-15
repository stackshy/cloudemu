package apprunner_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func TestVpcIngressConnectionLifecycle(t *testing.T) {
	m := newMock()
	public := mustNamed(t, m, "public-app")

	res, err := m.CreateService(bg, privateService("private-app"))
	requireNoError(t, err)

	cfg := driver.IngressVpcConfiguration{VpcID: "vpc-1a2b3c4d", VpcEndpointID: "vpce-1a2b3c4d"}

	// A public service cannot take a VPC ingress connection.
	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: public.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireInvalidRequest(t, err)

	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: "arn:aws:apprunner:us-east-1:123456789012:service/ghost/abc", IngressVpcConfiguration: cfg,
	})
	assertException(t, err, driver.ExResourceNotFound)

	for _, in := range []driver.CreateVpcIngressConnectionInput{
		{VpcIngressConnectionName: "abc", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg},
		{VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn},
	} {
		_, err = m.CreateVpcIngressConnection(bg, &in)
		requireInvalidRequest(t, err)
	}

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
		Tags: []driver.Tag{{Key: "env", Value: "dev"}},
	})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.IngressStatusAvailable)

	if !strings.Contains(conn.VpcIngressConnectionArn, ":vpcingressconnection/ingress-1/") || conn.DomainName == "" || conn.AccountID == "" {
		t.Fatalf("unexpected connection: %+v", conn)
	}

	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireInvalidRequest(t, err)

	got, err := m.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, got.IngressVpcConfiguration.VpcEndpointID, "vpce-1a2b3c4d")

	tags, err := m.ListTagsForResource(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)

	if len(tags) != 1 {
		t.Fatalf("ingress connection must be taggable, got %v", tags)
	}

	updated, err := m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, driver.IngressVpcConfiguration{VpcID: "vpc-9", VpcEndpointID: "vpce-9"})
	requireNoError(t, err)
	assertStr(t, updated.IngressVpcConfiguration.VpcID, "vpc-9")

	list, _, err := m.ListVpcIngressConnections(bg, driver.ListVpcIngressConnectionsFilter{ServiceArn: res.Service.ServiceArn}, driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 || list[0].VpcIngressConnectionArn != conn.VpcIngressConnectionArn {
		t.Fatalf("unexpected list: %+v", list)
	}

	none, _, _ := m.ListVpcIngressConnections(bg, driver.ListVpcIngressConnectionsFilter{VpcEndpointID: "vpce-1a2b3c4d"}, driver.Page{})
	if len(none) != 0 {
		t.Fatalf("filter by the old endpoint must find nothing after the update")
	}

	del, err := m.DeleteVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, del.Status, driver.IngressStatusDeleted)

	_, err = m.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	assertException(t, err, driver.ExResourceNotFound)

	// Deleting the service removes its connections.
	conn2, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-2", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireNoError(t, err)

	_, err = m.DeleteService(bg, res.Service.ServiceArn)
	requireNoError(t, err)

	_, err = m.DescribeVpcIngressConnection(bg, conn2.VpcIngressConnectionArn)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestVpcIngressConnectionAsyncStates(t *testing.T) {
	m, clk := newAsyncMock()

	res, err := m.CreateService(bg, privateService("async-private"))
	requireNoError(t, err)
	clk.Advance(time.Minute)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-a", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-1", VpcEndpointID: "vpce-1"},
	})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.IngressStatusPendingCreation)

	cfg := driver.IngressVpcConfiguration{VpcID: "vpc-2", VpcEndpointID: "vpce-2"}

	_, err = m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, cfg)
	assertException(t, err, driver.ExInvalidState)

	_, err = m.DeleteVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	assertException(t, err, driver.ExInvalidState)

	clk.Advance(time.Minute)

	up, err := m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, cfg)
	requireNoError(t, err)
	assertStr(t, up.Status, driver.IngressStatusPendingUpdate)
}
