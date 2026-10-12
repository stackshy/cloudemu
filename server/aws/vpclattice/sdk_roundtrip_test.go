package vpclattice_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsvpcl "github.com/aws/aws-sdk-go-v2/service/vpclattice"
	vpcltypes "github.com/aws/aws-sdk-go-v2/service/vpclattice/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	vpclatticesrv "github.com/stackshy/cloudemu/v2/server/aws/vpclattice"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	lattdriver "github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

func TestMatchesDoesNotShadowS3(t *testing.T) {
	h := vpclatticesrv.New(nil)

	cases := []struct {
		method, path string
		claim        bool
	}{
		{http.MethodGet, "/servicenetworks", true},                                            // Lattice ListServiceNetworks
		{http.MethodPost, "/services", true},                                                  // Lattice CreateService
		{http.MethodPatch, "/servicenetworks/sn-1", true},                                     // Lattice UpdateServiceNetwork (id-shaped)
		{http.MethodGet, "/services/svc-123", true},                                           // Lattice GetService (id-shaped)
		{http.MethodDelete, "/targetgroups/tg-1", true},                                       // Lattice DeleteTargetGroup (id-shaped)
		{http.MethodGet, "/tags/arn:aws:vpc-lattice:us-east-1:1:servicenetwork%2Fsn-1", true}, // ListTagsForResource
		{http.MethodPut, "/services/mykey", false},                                            // S3 PutObject into bucket "services"
		{http.MethodGet, "/services/mykey", false},                                            // S3 GetObject in bucket "services"
		{http.MethodDelete, "/targetgroups/mykey", false},                                     // S3 DeleteObject in bucket "targetgroups"
		{http.MethodGet, "/servicenetworks/mykey", false},                                     // S3 GetObject in bucket "servicenetworks"
		{http.MethodGet, "/tags", false},                                                      // S3 ListObjects on bucket "tags"
		{http.MethodGet, "/tags/mykey", false},                                                // S3 GetObject in bucket "tags"
		{http.MethodPost, "/tags", false},                                                     // S3 op on bucket "tags" (no ARN)
		{http.MethodGet, "/notalatticeroot", false},                                           // unrelated
	}

	for _, c := range cases {
		got := h.Matches(httptest.NewRequest(c.method, c.path, nil))
		if got != c.claim {
			t.Errorf("Matches(%s %s) = %v, want %v", c.method, c.path, got, c.claim)
		}
	}
}

// newClient builds an httptest-backed VPC Lattice client driving the real
// aws-sdk-go-v2 client against the in-memory driver.
func newClient(t *testing.T) *awsvpcl.Client {
	t.Helper()

	client, _, _ := newCloudClient(t)

	return client
}

// newCloudClient also returns the provider (to seed state through the driver)
// and the server URL (for raw HTTP checks).
func newCloudClient(t *testing.T) (*awsvpcl.Client, *awsprovider.Provider, string) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.DriversFrom(cloud))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsvpcl.NewFromConfig(cfg, func(o *awsvpcl.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	}), cloud, ts.URL
}

func TestSDKServiceNetworkLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{
		Name: aws.String("sn-app"),
	})
	if err != nil {
		t.Fatalf("CreateServiceNetwork: %v", err)
	}

	id := aws.ToString(created.Id)
	if id == "" || aws.ToString(created.Arn) == "" {
		t.Fatalf("CreateServiceNetwork: empty id/arn %+v", created)
	}

	got, err := client.GetServiceNetwork(ctx, &awsvpcl.GetServiceNetworkInput{
		ServiceNetworkIdentifier: aws.String(id),
	})
	if err != nil || aws.ToString(got.Name) != "sn-app" {
		t.Fatalf("GetServiceNetwork: %v %+v", err, got)
	}

	upd, err := client.UpdateServiceNetwork(ctx, &awsvpcl.UpdateServiceNetworkInput{
		ServiceNetworkIdentifier: aws.String(id),
		AuthType:                 vpcltypes.AuthTypeAwsIam,
	})
	if err != nil || upd.AuthType != vpcltypes.AuthTypeAwsIam {
		t.Fatalf("UpdateServiceNetwork: %v %+v", err, upd)
	}

	list, err := client.ListServiceNetworks(ctx, &awsvpcl.ListServiceNetworksInput{})
	if err != nil || len(list.Items) != 1 || aws.ToString(list.Items[0].Id) != id {
		t.Fatalf("ListServiceNetworks: %v %+v", err, list.Items)
	}

	if _, err = client.DeleteServiceNetwork(ctx, &awsvpcl.DeleteServiceNetworkInput{
		ServiceNetworkIdentifier: aws.String(id),
	}); err != nil {
		t.Fatalf("DeleteServiceNetwork: %v", err)
	}

	_, err = client.GetServiceNetwork(ctx, &awsvpcl.GetServiceNetworkInput{
		ServiceNetworkIdentifier: aws.String(id),
	})
	var nfe *vpcltypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %v", err)
	}
}

func TestSDKServiceLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{
		Name:             aws.String("svc-web"),
		CustomDomainName: aws.String("web.example.com"),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	id := aws.ToString(created.Id)
	if created.Status != vpcltypes.ServiceStatusActive || created.DnsEntry == nil {
		t.Fatalf("CreateService: status/dns %+v", created)
	}

	got, err := client.GetService(ctx, &awsvpcl.GetServiceInput{ServiceIdentifier: aws.String(id)})
	if err != nil || aws.ToString(got.Name) != "svc-web" || aws.ToInt32(got.IdleTimeoutSeconds) != 60 {
		t.Fatalf("GetService: %v %+v", err, got)
	}

	upd, err := client.UpdateService(ctx, &awsvpcl.UpdateServiceInput{
		ServiceIdentifier:  aws.String(id),
		AuthType:           vpcltypes.AuthTypeAwsIam,
		IdleTimeoutSeconds: aws.Int32(120),
	})
	if err != nil || upd.AuthType != vpcltypes.AuthTypeAwsIam || aws.ToInt32(upd.IdleTimeoutSeconds) != 120 {
		t.Fatalf("UpdateService: %v %+v", err, upd)
	}

	list, err := client.ListServices(ctx, &awsvpcl.ListServicesInput{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("ListServices: %v %+v", err, list.Items)
	}

	if _, err = client.DeleteService(ctx, &awsvpcl.DeleteServiceInput{ServiceIdentifier: aws.String(id)}); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	_, err = client.GetService(ctx, &awsvpcl.GetServiceInput{ServiceIdentifier: aws.String(id)})
	var nfe *vpcltypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestSDKListenerLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	svc, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc-l")})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	sid := aws.ToString(svc.Id)

	created, err := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: aws.String(sid),
		Name:              aws.String("http"),
		Protocol:          vpcltypes.ListenerProtocolHttp,
		Port:              aws.Int32(80),
		DefaultAction: &vpcltypes.RuleActionMemberFixedResponse{
			Value: vpcltypes.FixedResponseAction{StatusCode: aws.Int32(404)},
		},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}
	lid := aws.ToString(created.Id)

	got, err := client.GetListener(ctx, &awsvpcl.GetListenerInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
	})
	if err != nil || aws.ToInt32(got.Port) != 80 || got.Protocol != vpcltypes.ListenerProtocolHttp {
		t.Fatalf("GetListener: %v %+v", err, got)
	}
	fr, ok := got.DefaultAction.(*vpcltypes.RuleActionMemberFixedResponse)
	if !ok || aws.ToInt32(fr.Value.StatusCode) != 404 {
		t.Fatalf("GetListener defaultAction round-trip: %+v", got.DefaultAction)
	}

	if _, err = client.UpdateListener(ctx, &awsvpcl.UpdateListenerInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
		DefaultAction: &vpcltypes.RuleActionMemberFixedResponse{
			Value: vpcltypes.FixedResponseAction{StatusCode: aws.Int32(500)},
		},
	}); err != nil {
		t.Fatalf("UpdateListener: %v", err)
	}

	list, err := client.ListListeners(ctx, &awsvpcl.ListListenersInput{ServiceIdentifier: aws.String(sid)})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("ListListeners: %v %+v", err, list.Items)
	}

	if _, err = client.DeleteListener(ctx, &awsvpcl.DeleteListenerInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
	}); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}

	// Listener scoped to unknown service → ResourceNotFoundException.
	_, err = client.GetListener(ctx, &awsvpcl.GetListenerInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
	})
	var nfe *vpcltypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestSDKRuleLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	svc, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc-r")})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	sid := aws.ToString(svc.Id)

	ln, err := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: aws.String(sid), Name: aws.String("http"),
		Protocol: vpcltypes.ListenerProtocolHttp, Port: aws.Int32(80),
		DefaultAction: &vpcltypes.RuleActionMemberFixedResponse{
			Value: vpcltypes.FixedResponseAction{StatusCode: aws.Int32(404)},
		},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}
	lid := aws.ToString(ln.Id)

	match := &vpcltypes.RuleMatchMemberHttpMatch{Value: vpcltypes.HttpMatch{
		PathMatch: &vpcltypes.PathMatch{Match: &vpcltypes.PathMatchTypeMemberExact{Value: "/api"}},
	}}
	action := &vpcltypes.RuleActionMemberFixedResponse{Value: vpcltypes.FixedResponseAction{StatusCode: aws.Int32(200)}}

	created, err := client.CreateRule(ctx, &awsvpcl.CreateRuleInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
		Name: aws.String("r1"), Priority: aws.Int32(10), Match: match, Action: action,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	rid := aws.ToString(created.Id)

	got, err := client.GetRule(ctx, &awsvpcl.GetRuleInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid), RuleIdentifier: aws.String(rid),
	})
	if err != nil || aws.ToInt32(got.Priority) != 10 {
		t.Fatalf("GetRule: %v %+v", err, got)
	}
	gm, ok := got.Match.(*vpcltypes.RuleMatchMemberHttpMatch)
	if !ok || gm.Value.PathMatch == nil {
		t.Fatalf("GetRule match round-trip: %+v", got.Match)
	}

	if _, err = client.UpdateRule(ctx, &awsvpcl.UpdateRuleInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid), RuleIdentifier: aws.String(rid),
		Priority: aws.Int32(20),
	}); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}

	list, err := client.ListRules(ctx, &awsvpcl.ListRulesInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
	})
	if err != nil || len(list.Items) != 2 { // the custom rule + the default rule
		t.Fatalf("ListRules: %v %+v", err, list.Items)
	}

	// BatchUpdateRule: one success, one failure (unknown id).
	bur, err := client.BatchUpdateRule(ctx, &awsvpcl.BatchUpdateRuleInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid),
		Rules: []vpcltypes.RuleUpdate{
			{RuleIdentifier: aws.String(rid), Priority: aws.Int32(30)},
			{RuleIdentifier: aws.String("rule-missing"), Priority: aws.Int32(40)},
		},
	})
	if err != nil || len(bur.Successful) != 1 || len(bur.Unsuccessful) != 1 {
		t.Fatalf("BatchUpdateRule: %v succ=%+v fail=%+v", err, bur.Successful, bur.Unsuccessful)
	}

	if _, err = client.DeleteRule(ctx, &awsvpcl.DeleteRuleInput{
		ServiceIdentifier: aws.String(sid), ListenerIdentifier: aws.String(lid), RuleIdentifier: aws.String(rid),
	}); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
}

func TestSDKTargetGroupAndTargets(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateTargetGroup(ctx, &awsvpcl.CreateTargetGroupInput{
		Name: aws.String("tg-1"),
		Type: vpcltypes.TargetGroupTypeIp,
		Config: &vpcltypes.TargetGroupConfig{
			Port:          aws.Int32(443),
			Protocol:      vpcltypes.TargetGroupProtocolHttps,
			VpcIdentifier: aws.String("vpc-123"),
		},
	})
	if err != nil || created.Config == nil {
		t.Fatalf("CreateTargetGroup: %v %+v", err, created)
	}
	id := aws.ToString(created.Id)

	got, err := client.GetTargetGroup(ctx, &awsvpcl.GetTargetGroupInput{TargetGroupIdentifier: aws.String(id)})
	if err != nil || aws.ToInt32(got.Config.Port) != 443 || got.Config.Protocol != vpcltypes.TargetGroupProtocolHttps {
		t.Fatalf("GetTargetGroup config round-trip: %v %+v", err, got)
	}

	if _, err = client.UpdateTargetGroup(ctx, &awsvpcl.UpdateTargetGroupInput{
		TargetGroupIdentifier: aws.String(id),
		HealthCheck:           &vpcltypes.HealthCheckConfig{Enabled: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("UpdateTargetGroup: %v", err)
	}

	reg, err := client.RegisterTargets(ctx, &awsvpcl.RegisterTargetsInput{
		TargetGroupIdentifier: aws.String(id),
		Targets: []vpcltypes.Target{
			{Id: aws.String("10.0.0.1"), Port: aws.Int32(443)},
			{Id: aws.String("10.0.0.2"), Port: aws.Int32(443)},
		},
	})
	if err != nil || len(reg.Successful) != 2 {
		t.Fatalf("RegisterTargets: %v %+v", err, reg.Successful)
	}

	lt, err := client.ListTargets(ctx, &awsvpcl.ListTargetsInput{TargetGroupIdentifier: aws.String(id)})
	if err != nil || len(lt.Items) != 2 {
		t.Fatalf("ListTargets: %v %+v", err, lt.Items)
	}

	if _, err = client.DeregisterTargets(ctx, &awsvpcl.DeregisterTargetsInput{
		TargetGroupIdentifier: aws.String(id),
		Targets:               []vpcltypes.Target{{Id: aws.String("10.0.0.1"), Port: aws.Int32(443)}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}

	lt, err = client.ListTargets(ctx, &awsvpcl.ListTargetsInput{TargetGroupIdentifier: aws.String(id)})
	if err != nil || len(lt.Items) != 1 {
		t.Fatalf("ListTargets after deregister: %v %+v", err, lt.Items)
	}

	tgs, err := client.ListTargetGroups(ctx, &awsvpcl.ListTargetGroupsInput{})
	if err != nil || len(tgs.Items) != 1 || aws.ToInt32(tgs.Items[0].Port) != 443 {
		t.Fatalf("ListTargetGroups summary: %v %+v", err, tgs.Items)
	}

	if _, err = client.DeleteTargetGroup(ctx, &awsvpcl.DeleteTargetGroupInput{TargetGroupIdentifier: aws.String(id)}); err != nil {
		t.Fatalf("DeleteTargetGroup: %v", err)
	}
}

func TestSDKAssociations(t *testing.T) {
	client, cloud, _ := newCloudClient(t)
	ctx := context.Background()

	vpcInfo, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	vpcID := vpcInfo.ID

	sn, err := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn-a")})
	if err != nil {
		t.Fatalf("CreateServiceNetwork: %v", err)
	}
	snID := aws.ToString(sn.Id)

	svc, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc-a")})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	svcID := aws.ToString(svc.Id)

	// SN ↔ VPC
	vpcA, err := client.CreateServiceNetworkVpcAssociation(ctx, &awsvpcl.CreateServiceNetworkVpcAssociationInput{
		ServiceNetworkIdentifier: aws.String(snID),
		VpcIdentifier:            aws.String(vpcID),
		SecurityGroupIds:         []string{"sg-1"},
	})
	if err != nil {
		t.Fatalf("CreateSNVpcAssociation: %v", err)
	}
	vpcAID := aws.ToString(vpcA.Id)

	if _, err = client.UpdateServiceNetworkVpcAssociation(ctx, &awsvpcl.UpdateServiceNetworkVpcAssociationInput{
		ServiceNetworkVpcAssociationIdentifier: aws.String(vpcAID),
		SecurityGroupIds:                       []string{"sg-1", "sg-2"},
	}); err != nil {
		t.Fatalf("UpdateSNVpcAssociation: %v", err)
	}

	gotVpc, err := client.GetServiceNetworkVpcAssociation(ctx, &awsvpcl.GetServiceNetworkVpcAssociationInput{
		ServiceNetworkVpcAssociationIdentifier: aws.String(vpcAID),
	})
	if err != nil || aws.ToString(gotVpc.VpcId) != vpcID || len(gotVpc.SecurityGroupIds) != 2 {
		t.Fatalf("GetSNVpcAssociation: %v %+v", err, gotVpc)
	}

	// SN ↔ Service
	svcA, err := client.CreateServiceNetworkServiceAssociation(ctx, &awsvpcl.CreateServiceNetworkServiceAssociationInput{
		ServiceNetworkIdentifier: aws.String(snID), ServiceIdentifier: aws.String(svcID),
	})
	if err != nil {
		t.Fatalf("CreateSNServiceAssociation: %v", err)
	}

	// SN ↔ Resource
	resA, err := client.CreateServiceNetworkResourceAssociation(ctx, &awsvpcl.CreateServiceNetworkResourceAssociationInput{
		ServiceNetworkIdentifier:        aws.String(snID),
		ResourceConfigurationIdentifier: aws.String("rc-1"),
	})
	if err != nil {
		t.Fatalf("CreateSNResourceAssociation: %v", err)
	}

	// Counts reflected on the service network.
	gotSN, err := client.GetServiceNetwork(ctx, &awsvpcl.GetServiceNetworkInput{ServiceNetworkIdentifier: aws.String(snID)})
	if err != nil || aws.ToInt64(gotSN.NumberOfAssociatedServices) != 1 || aws.ToInt64(gotSN.NumberOfAssociatedVPCs) != 1 {
		t.Fatalf("GetServiceNetwork counts: %v svc=%d vpc=%d", err,
			aws.ToInt64(gotSN.NumberOfAssociatedServices), aws.ToInt64(gotSN.NumberOfAssociatedVPCs))
	}

	if lv, e := client.ListServiceNetworkVpcAssociations(ctx, &awsvpcl.ListServiceNetworkVpcAssociationsInput{}); e != nil || len(lv.Items) != 1 {
		t.Fatalf("ListSNVpcAssociations: %v %+v", e, lv)
	}
	if _, e := client.ListServiceNetworkVpcEndpointAssociations(ctx, &awsvpcl.ListServiceNetworkVpcEndpointAssociationsInput{ServiceNetworkIdentifier: aws.String(snID)}); e != nil {
		t.Fatalf("ListSNVpcEndpointAssociations: %v", e)
	}
	if ls, e := client.ListServiceNetworkServiceAssociations(ctx, &awsvpcl.ListServiceNetworkServiceAssociationsInput{}); e != nil || len(ls.Items) != 1 {
		t.Fatalf("ListSNServiceAssociations: %v %+v", e, ls)
	}
	if lr, e := client.ListServiceNetworkResourceAssociations(ctx, &awsvpcl.ListServiceNetworkResourceAssociationsInput{}); e != nil || len(lr.Items) != 1 {
		t.Fatalf("ListSNResourceAssociations: %v %+v", e, lr)
	}

	// Get by id (wire-level coverage for both association Get paths).
	if gs, e := client.GetServiceNetworkServiceAssociation(ctx, &awsvpcl.GetServiceNetworkServiceAssociationInput{
		ServiceNetworkServiceAssociationIdentifier: svcA.Id,
	}); e != nil || aws.ToString(gs.ServiceId) != svcID {
		t.Fatalf("GetSNServiceAssociation: %v %+v", e, gs)
	}
	if _, e := client.GetServiceNetworkResourceAssociation(ctx, &awsvpcl.GetServiceNetworkResourceAssociationInput{
		ServiceNetworkResourceAssociationIdentifier: resA.Id,
	}); e != nil {
		t.Fatalf("GetSNResourceAssociation: %v", e)
	}

	// ResourceEndpointAssociations aren't synthesized by the emulator, so a
	// delete resolves to NotFound. This exercises the otherwise-unreachable handler.
	_, e := client.DeleteResourceEndpointAssociation(ctx, &awsvpcl.DeleteResourceEndpointAssociationInput{
		ResourceEndpointAssociationIdentifier: aws.String("rea-none"),
	})
	var nfe *vpcltypes.ResourceNotFoundException
	if !errors.As(e, &nfe) {
		t.Fatalf("DeleteResourceEndpointAssociation: expected ResourceNotFoundException, got %v", e)
	}

	// Deletes.
	if _, e := client.DeleteServiceNetworkVpcAssociation(ctx, &awsvpcl.DeleteServiceNetworkVpcAssociationInput{ServiceNetworkVpcAssociationIdentifier: aws.String(vpcAID)}); e != nil {
		t.Fatalf("DeleteSNVpcAssociation: %v", e)
	}
	if _, e := client.DeleteServiceNetworkServiceAssociation(ctx, &awsvpcl.DeleteServiceNetworkServiceAssociationInput{ServiceNetworkServiceAssociationIdentifier: svcA.Id}); e != nil {
		t.Fatalf("DeleteSNServiceAssociation: %v", e)
	}
	if _, e := client.DeleteServiceNetworkResourceAssociation(ctx, &awsvpcl.DeleteServiceNetworkResourceAssociationInput{ServiceNetworkResourceAssociationIdentifier: resA.Id}); e != nil {
		t.Fatalf("DeleteSNResourceAssociation: %v", e)
	}
}

func TestSDKResourceConfigGatewayEndpoint(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	gw, err := client.CreateResourceGateway(ctx, &awsvpcl.CreateResourceGatewayInput{
		Name:             aws.String("rg-1"),
		VpcIdentifier:    aws.String("vpc-1"),
		SubnetIds:        []string{"subnet-1"},
		SecurityGroupIds: []string{"sg-1"},
		IpAddressType:    vpcltypes.ResourceGatewayIpAddressTypeIpv4,
	})
	if err != nil {
		t.Fatalf("CreateResourceGateway: %v", err)
	}
	gwID := aws.ToString(gw.Id)

	if _, err = client.UpdateResourceGateway(ctx, &awsvpcl.UpdateResourceGatewayInput{
		ResourceGatewayIdentifier: aws.String(gwID), SecurityGroupIds: []string{"sg-1", "sg-2"},
	}); err != nil {
		t.Fatalf("UpdateResourceGateway: %v", err)
	}

	gotGw, err := client.GetResourceGateway(ctx, &awsvpcl.GetResourceGatewayInput{ResourceGatewayIdentifier: aws.String(gwID)})
	if err != nil || len(gotGw.SecurityGroupIds) != 2 {
		t.Fatalf("GetResourceGateway: %v %+v", err, gotGw)
	}

	rc, err := client.CreateResourceConfiguration(ctx, &awsvpcl.CreateResourceConfigurationInput{
		Name:                      aws.String("rc-1"),
		Type:                      vpcltypes.ResourceConfigurationTypeSingle,
		Protocol:                  vpcltypes.ProtocolTypeTcp,
		PortRanges:                []string{"443"},
		ResourceGatewayIdentifier: aws.String(gwID),
		ResourceConfigurationDefinition: &vpcltypes.ResourceConfigurationDefinitionMemberIpResource{
			Value: vpcltypes.IpResource{IpAddress: aws.String("10.0.0.9")},
		},
	})
	if err != nil {
		t.Fatalf("CreateResourceConfiguration: %v", err)
	}
	rcID := aws.ToString(rc.Id)

	gotRc, err := client.GetResourceConfiguration(ctx, &awsvpcl.GetResourceConfigurationInput{
		ResourceConfigurationIdentifier: aws.String(rcID),
	})
	if err != nil || gotRc.Type != vpcltypes.ResourceConfigurationTypeSingle {
		t.Fatalf("GetResourceConfiguration: %v %+v", err, gotRc)
	}
	def, ok := gotRc.ResourceConfigurationDefinition.(*vpcltypes.ResourceConfigurationDefinitionMemberIpResource)
	if !ok || aws.ToString(def.Value.IpAddress) != "10.0.0.9" {
		t.Fatalf("ResourceConfigurationDefinition round-trip: %+v", gotRc.ResourceConfigurationDefinition)
	}

	if _, err = client.UpdateResourceConfiguration(ctx, &awsvpcl.UpdateResourceConfigurationInput{
		ResourceConfigurationIdentifier: aws.String(rcID), PortRanges: []string{"443", "8443"},
	}); err != nil {
		t.Fatalf("UpdateResourceConfiguration: %v", err)
	}

	if lc, e := client.ListResourceConfigurations(ctx, &awsvpcl.ListResourceConfigurationsInput{}); e != nil || len(lc.Items) != 1 {
		t.Fatalf("ListResourceConfigurations: %v %+v", e, lc)
	}
	if lg, e := client.ListResourceGateways(ctx, &awsvpcl.ListResourceGatewaysInput{}); e != nil || len(lg.Items) != 1 {
		t.Fatalf("ListResourceGateways: %v %+v", e, lg)
	}
	if le, e := client.ListResourceEndpointAssociations(ctx, &awsvpcl.ListResourceEndpointAssociationsInput{ResourceConfigurationIdentifier: aws.String(rcID)}); e != nil || len(le.Items) != 0 {
		t.Fatalf("ListResourceEndpointAssociations: %v %+v", e, le)
	}

	if _, e := client.DeleteResourceConfiguration(ctx, &awsvpcl.DeleteResourceConfigurationInput{ResourceConfigurationIdentifier: aws.String(rcID)}); e != nil {
		t.Fatalf("DeleteResourceConfiguration: %v", e)
	}
	if _, e := client.DeleteResourceGateway(ctx, &awsvpcl.DeleteResourceGatewayInput{ResourceGatewayIdentifier: aws.String(gwID)}); e != nil {
		t.Fatalf("DeleteResourceGateway: %v", e)
	}
}

func TestSDKAccessLogsPoliciesDomainsTags(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	sn, err := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn-x")})
	if err != nil {
		t.Fatalf("CreateServiceNetwork: %v", err)
	}
	snArn := aws.ToString(sn.Arn)

	// Access-log subscription
	als, err := client.CreateAccessLogSubscription(ctx, &awsvpcl.CreateAccessLogSubscriptionInput{
		ResourceIdentifier: aws.String(snArn),
		DestinationArn:     aws.String("arn:aws:logs:us-east-1:123456789012:log-group:lattice"),
	})
	if err != nil {
		t.Fatalf("CreateAccessLogSubscription: %v", err)
	}
	alsID := aws.ToString(als.Id)
	if _, err = client.UpdateAccessLogSubscription(ctx, &awsvpcl.UpdateAccessLogSubscriptionInput{
		AccessLogSubscriptionIdentifier: aws.String(alsID),
		DestinationArn:                  aws.String("arn:aws:s3:::lattice-logs"),
	}); err != nil {
		t.Fatalf("UpdateAccessLogSubscription: %v", err)
	}
	if _, err = client.GetAccessLogSubscription(ctx, &awsvpcl.GetAccessLogSubscriptionInput{AccessLogSubscriptionIdentifier: aws.String(alsID)}); err != nil {
		t.Fatalf("GetAccessLogSubscription: %v", err)
	}
	if la, e := client.ListAccessLogSubscriptions(ctx, &awsvpcl.ListAccessLogSubscriptionsInput{ResourceIdentifier: aws.String(snArn)}); e != nil || len(la.Items) != 1 {
		t.Fatalf("ListAccessLogSubscriptions: %v %+v", e, la)
	}

	// Auth policy (keyed by service-network ARN)
	pa, err := client.PutAuthPolicy(ctx, &awsvpcl.PutAuthPolicyInput{
		ResourceIdentifier: aws.String(snArn), Policy: aws.String(`{"Version":"2012-10-17"}`),
	})
	if err != nil || pa.State != vpcltypes.AuthPolicyStateActive {
		t.Fatalf("PutAuthPolicy: %v %+v", err, pa)
	}
	ga, err := client.GetAuthPolicy(ctx, &awsvpcl.GetAuthPolicyInput{ResourceIdentifier: aws.String(snArn)})
	if err != nil || aws.ToString(ga.Policy) != `{"Version":"2012-10-17"}` {
		t.Fatalf("GetAuthPolicy: %v %+v", err, ga)
	}

	// Resource policy
	if _, err = client.PutResourcePolicy(ctx, &awsvpcl.PutResourcePolicyInput{
		ResourceArn: aws.String(snArn), Policy: aws.String(`{"rp":true}`),
	}); err != nil {
		t.Fatalf("PutResourcePolicy: %v", err)
	}
	gr, err := client.GetResourcePolicy(ctx, &awsvpcl.GetResourcePolicyInput{ResourceArn: aws.String(snArn)})
	if err != nil || aws.ToString(gr.Policy) != `{"rp":true}` {
		t.Fatalf("GetResourcePolicy: %v %+v", err, gr)
	}

	// Domain verification
	dv, err := client.StartDomainVerification(ctx, &awsvpcl.StartDomainVerificationInput{DomainName: aws.String("example.com")})
	if err != nil || dv.Status != vpcltypes.VerificationStatusPending {
		t.Fatalf("StartDomainVerification: %v %+v", err, dv)
	}
	dvID := aws.ToString(dv.Id)
	if _, err = client.GetDomainVerification(ctx, &awsvpcl.GetDomainVerificationInput{DomainVerificationIdentifier: aws.String(dvID)}); err != nil {
		t.Fatalf("GetDomainVerification: %v", err)
	}
	if ld, e := client.ListDomainVerifications(ctx, &awsvpcl.ListDomainVerificationsInput{}); e != nil || len(ld.Items) != 1 {
		t.Fatalf("ListDomainVerifications: %v %+v", e, ld)
	}

	// Tagging (ARN contains slashes)
	if _, err = client.TagResource(ctx, &awsvpcl.TagResourceInput{
		ResourceArn: aws.String(snArn), Tags: map[string]string{"team": "net", "env": "test"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}
	lt, err := client.ListTagsForResource(ctx, &awsvpcl.ListTagsForResourceInput{ResourceArn: aws.String(snArn)})
	if err != nil || len(lt.Tags) != 2 {
		t.Fatalf("ListTagsForResource: %v %+v", err, lt.Tags)
	}
	if _, err = client.UntagResource(ctx, &awsvpcl.UntagResourceInput{ResourceArn: aws.String(snArn), TagKeys: []string{"team"}}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}
	lt, err = client.ListTagsForResource(ctx, &awsvpcl.ListTagsForResourceInput{ResourceArn: aws.String(snArn)})
	if err != nil || len(lt.Tags) != 1 {
		t.Fatalf("ListTagsForResource after untag: %v %+v", err, lt.Tags)
	}

	// Deletes.
	if _, e := client.DeleteAccessLogSubscription(ctx, &awsvpcl.DeleteAccessLogSubscriptionInput{AccessLogSubscriptionIdentifier: aws.String(alsID)}); e != nil {
		t.Fatalf("DeleteAccessLogSubscription: %v", e)
	}
	if _, e := client.DeleteAuthPolicy(ctx, &awsvpcl.DeleteAuthPolicyInput{ResourceIdentifier: aws.String(snArn)}); e != nil {
		t.Fatalf("DeleteAuthPolicy: %v", e)
	}
	if _, e := client.DeleteResourcePolicy(ctx, &awsvpcl.DeleteResourcePolicyInput{ResourceArn: aws.String(snArn)}); e != nil {
		t.Fatalf("DeleteResourcePolicy: %v", e)
	}
	if _, e := client.DeleteDomainVerification(ctx, &awsvpcl.DeleteDomainVerificationInput{DomainVerificationIdentifier: aws.String(dvID)}); e != nil {
		t.Fatalf("DeleteDomainVerification: %v", e)
	}
}

// --- #1451 real-cloud parity ---

// requireAPIError asserts err unwraps to the typed SDK exception T (i.e. the
// exception is one the operation models) and returns it.
func requireAPIError[T error](t *testing.T, err error, label string) T {
	t.Helper()

	var typed T
	if !errors.As(err, &typed) {
		t.Fatalf("%s: want %T, got %v", label, typed, err)
	}

	return typed
}

func httpStatus(t *testing.T, err error) int {
	t.Helper()

	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("not an HTTP response error: %v", err)
	}

	return re.HTTPStatusCode()
}

func fwdAction(tgID string) vpcltypes.RuleAction {
	return &vpcltypes.RuleActionMemberForward{Value: vpcltypes.ForwardAction{
		TargetGroups: []vpcltypes.WeightedTargetGroup{{TargetGroupIdentifier: aws.String(tgID), Weight: aws.Int32(1)}},
	}}
}

func fixed404() vpcltypes.RuleAction {
	return &vpcltypes.RuleActionMemberFixedResponse{Value: vpcltypes.FixedResponseAction{StatusCode: aws.Int32(404)}}
}

func TestSDK_CreateTargetGroup_InvalidType(t *testing.T) {
	client := newClient(t)

	_, err := client.CreateTargetGroup(context.Background(), &awsvpcl.CreateTargetGroupInput{
		Name: aws.String("bad"), Type: vpcltypes.TargetGroupType("BOGUS"),
	})
	requireAPIError[*vpcltypes.ValidationException](t, err, "BOGUS type")

	if code := httpStatus(t, err); code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
}

func TestSDK_DeleteTargetGroup_InUse_And_ServiceArns(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	tg, err := client.CreateTargetGroup(ctx, &awsvpcl.CreateTargetGroupInput{Name: aws.String("tg"), Type: vpcltypes.TargetGroupTypeIp})
	if err != nil {
		t.Fatal(err)
	}

	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc")})
	lis, err := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttp,
		DefaultAction: fixed404(),
	})
	if err != nil {
		t.Fatal(err)
	}

	rule, err := client.CreateRule(ctx, &awsvpcl.CreateRuleInput{
		ServiceIdentifier: svc.Id, ListenerIdentifier: lis.Id, Name: aws.String("r"), Priority: aws.Int32(1),
		Match:  &vpcltypes.RuleMatchMemberHttpMatch{Value: vpcltypes.HttpMatch{Method: aws.String("GET")}},
		Action: fwdAction(aws.ToString(tg.Id)),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.GetTargetGroup(ctx, &awsvpcl.GetTargetGroupInput{TargetGroupIdentifier: tg.Id})
	if err != nil || len(got.ServiceArns) != 1 || got.ServiceArns[0] != aws.ToString(svc.Arn) {
		t.Fatalf("GetTargetGroup serviceArns: %v %+v", err, got)
	}

	lst, err := client.ListTargetGroups(ctx, &awsvpcl.ListTargetGroupsInput{})
	if err != nil || len(lst.Items) != 1 || len(lst.Items[0].ServiceArns) != 1 {
		t.Fatalf("ListTargetGroups serviceArns: %v %+v", err, lst)
	}

	_, err = client.DeleteTargetGroup(ctx, &awsvpcl.DeleteTargetGroupInput{TargetGroupIdentifier: tg.Id})
	requireAPIError[*vpcltypes.ConflictException](t, err, "delete in-use TG")

	if code := httpStatus(t, err); code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}

	if _, err = client.DeleteRule(ctx, &awsvpcl.DeleteRuleInput{
		ServiceIdentifier: svc.Id, ListenerIdentifier: lis.Id, RuleIdentifier: rule.Id,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = client.DeleteTargetGroup(ctx, &awsvpcl.DeleteTargetGroupInput{TargetGroupIdentifier: tg.Id}); err != nil {
		t.Fatalf("delete after rule removed: %v", err)
	}
}

func TestSDK_CreateListener_Validation(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc")})

	_, err := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocol("FTP"),
		DefaultAction: fixed404(),
	})
	requireAPIError[*vpcltypes.ValidationException](t, err, "FTP")

	_, err = client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttp,
		Port: aws.Int32(999999), DefaultAction: fixed404(),
	})
	requireAPIError[*vpcltypes.ValidationException](t, err, "port 999999")

	l, err := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttps,
		DefaultAction: fixed404(),
	})
	if err != nil || aws.ToInt32(l.Port) != 443 {
		t.Fatalf("HTTPS default port: %v %+v", err, l)
	}
}

func TestSDK_AuthType_Invalid(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	bad := vpcltypes.AuthType("BOGUS")

	_, err := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn"), AuthType: bad})
	requireAPIError[*vpcltypes.ValidationException](t, err, "CreateServiceNetwork")

	_, err = client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc"), AuthType: bad})
	requireAPIError[*vpcltypes.ValidationException](t, err, "CreateService")

	sn, _ := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn")})
	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc")})

	_, err = client.UpdateServiceNetwork(ctx, &awsvpcl.UpdateServiceNetworkInput{ServiceNetworkIdentifier: sn.Id, AuthType: bad})
	requireAPIError[*vpcltypes.ValidationException](t, err, "UpdateServiceNetwork")

	_, err = client.UpdateService(ctx, &awsvpcl.UpdateServiceInput{ServiceIdentifier: svc.Id, AuthType: bad})
	requireAPIError[*vpcltypes.ValidationException](t, err, "UpdateService")
}

func TestSDK_Duplicates(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	if _, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("dup")}); err != nil {
		t.Fatal(err)
	}

	_, err := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("dup")})
	requireAPIError[*vpcltypes.ConflictException](t, err, "dup service")

	if code := httpStatus(t, err); code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}

	sn, err := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("dup")})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("dup")})
	requireAPIError[*vpcltypes.ConflictException](t, err, "dup SN")

	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("other")})
	if _, err = client.CreateServiceNetworkServiceAssociation(ctx, &awsvpcl.CreateServiceNetworkServiceAssociationInput{
		ServiceNetworkIdentifier: sn.Id, ServiceIdentifier: svc.Id,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateServiceNetworkServiceAssociation(ctx, &awsvpcl.CreateServiceNetworkServiceAssociationInput{
		ServiceNetworkIdentifier: sn.Id, ServiceIdentifier: svc.Id,
	})
	requireAPIError[*vpcltypes.ConflictException](t, err, "dup SN-service association")

	// Duplicate rule priority on the same listener.
	l, _ := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttp, DefaultAction: fixed404(),
	})
	mk := func(name string) error {
		_, e := client.CreateRule(ctx, &awsvpcl.CreateRuleInput{
			ServiceIdentifier: svc.Id, ListenerIdentifier: l.Id, Name: aws.String(name), Priority: aws.Int32(5),
			Match:  &vpcltypes.RuleMatchMemberHttpMatch{Value: vpcltypes.HttpMatch{Method: aws.String("GET")}},
			Action: fixed404(),
		})

		return e
	}

	if err = mk("r1"); err != nil {
		t.Fatal(err)
	}

	requireAPIError[*vpcltypes.ConflictException](t, mk("r2"), "dup rule priority")
}

func TestSDK_SNVpcAssociation_UnknownVPC(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	sn, _ := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn")})

	_, err := client.CreateServiceNetworkVpcAssociation(ctx, &awsvpcl.CreateServiceNetworkVpcAssociationInput{
		ServiceNetworkIdentifier: sn.Id, VpcIdentifier: aws.String("vpc-doesnotexist"),
	})
	requireAPIError[*vpcltypes.ResourceNotFoundException](t, err, "unknown VPC")

	if code := httpStatus(t, err); code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
}

func TestSDK_AccessLogSubscription_UnknownResource(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.CreateAccessLogSubscription(ctx, &awsvpcl.CreateAccessLogSubscriptionInput{
		ResourceIdentifier: aws.String("sn-doesnotexist"), DestinationArn: aws.String("arn:aws:s3:::bucket"),
	})
	requireAPIError[*vpcltypes.ResourceNotFoundException](t, err, "unknown resource")

	sn, _ := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn")})
	if _, err = client.CreateAccessLogSubscription(ctx, &awsvpcl.CreateAccessLogSubscriptionInput{
		ResourceIdentifier: sn.Arn, DestinationArn: aws.String("arn:aws:s3:::bucket"),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSDK_DeleteResourceGateway_InUse(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	gw, err := client.CreateResourceGateway(ctx, &awsvpcl.CreateResourceGatewayInput{
		Name: aws.String("gw"), VpcIdentifier: aws.String("vpc-1"), SubnetIds: []string{"subnet-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rc, err := client.CreateResourceConfiguration(ctx, &awsvpcl.CreateResourceConfigurationInput{
		Name: aws.String("rc"), Type: vpcltypes.ResourceConfigurationTypeSingle, ResourceGatewayIdentifier: gw.Id,
		ResourceConfigurationDefinition: &vpcltypes.ResourceConfigurationDefinitionMemberIpResource{
			Value: vpcltypes.IpResource{IpAddress: aws.String("10.0.0.9")},
		},
		PortRanges: []string{"443"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.DeleteResourceGateway(ctx, &awsvpcl.DeleteResourceGatewayInput{ResourceGatewayIdentifier: gw.Id})
	requireAPIError[*vpcltypes.ConflictException](t, err, "gateway in use")

	if _, err = client.DeleteResourceConfiguration(ctx, &awsvpcl.DeleteResourceConfigurationInput{
		ResourceConfigurationIdentifier: rc.Id,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = client.DeleteResourceGateway(ctx, &awsvpcl.DeleteResourceGatewayInput{ResourceGatewayIdentifier: gw.Id}); err != nil {
		t.Fatalf("delete after config removed: %v", err)
	}
}

func TestSDK_ListRules_DefaultRule(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc")})
	l, _ := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttp, DefaultAction: fixed404(),
	})

	rs, err := client.ListRules(ctx, &awsvpcl.ListRulesInput{ServiceIdentifier: svc.Id, ListenerIdentifier: l.Id})
	if err != nil || len(rs.Items) != 1 || !aws.ToBool(rs.Items[0].IsDefault) {
		t.Fatalf("ListRules: %v %+v", err, rs)
	}

	_, err = client.DeleteRule(ctx, &awsvpcl.DeleteRuleInput{
		ServiceIdentifier: svc.Id, ListenerIdentifier: l.Id, RuleIdentifier: rs.Items[0].Id,
	})
	requireAPIError[*vpcltypes.ValidationException](t, err, "delete default rule")
}

func TestSDK_PutAuthPolicy_InvalidJSON(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	sn, _ := client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("sn")})

	_, err := client.PutAuthPolicy(ctx, &awsvpcl.PutAuthPolicyInput{ResourceIdentifier: sn.Id, Policy: aws.String("{bad")})
	requireAPIError[*vpcltypes.ValidationException](t, err, "malformed policy")

	if _, err = client.PutAuthPolicy(ctx, &awsvpcl.PutAuthPolicyInput{
		ResourceIdentifier: sn.Id, Policy: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSDK_BatchUpdateRule_Failure(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	svc, _ := client.CreateService(ctx, &awsvpcl.CreateServiceInput{Name: aws.String("svc")})
	l, _ := client.CreateListener(ctx, &awsvpcl.CreateListenerInput{
		ServiceIdentifier: svc.Id, Name: aws.String("l"), Protocol: vpcltypes.ListenerProtocolHttp, DefaultAction: fixed404(),
	})

	out, err := client.BatchUpdateRule(ctx, &awsvpcl.BatchUpdateRuleInput{
		ServiceIdentifier: svc.Id, ListenerIdentifier: l.Id,
		Rules: []vpcltypes.RuleUpdate{{RuleIdentifier: aws.String("rule-missing"), Priority: aws.Int32(3)}},
	})
	if err != nil || len(out.Unsuccessful) != 1 {
		t.Fatalf("BatchUpdateRule: %v %+v", err, out)
	}

	f := out.Unsuccessful[0]
	if aws.ToString(f.FailureCode) != "ResourceNotFoundException" || strings.Contains(aws.ToString(f.FailureMessage), "NotFound:") {
		t.Fatalf("failure shape: %+v", f)
	}
}

// TestOnlyModeledExceptions drives rejections and requires the SDK to decode a
// typed exception: one the operation does not model would surface as a generic
// smithy error instead.
func TestOnlyModeledExceptions(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	cases := map[string]error{}
	_, cases["CreateTargetGroup"] = client.CreateTargetGroup(ctx, &awsvpcl.CreateTargetGroupInput{Name: aws.String("a"), Type: "X"})
	_, cases["CreateServiceNetwork"] = client.CreateServiceNetwork(ctx, &awsvpcl.CreateServiceNetworkInput{Name: aws.String("a"), AuthType: "X"})
	_, cases["PutAuthPolicy"] = client.PutAuthPolicy(ctx, &awsvpcl.PutAuthPolicyInput{ResourceIdentifier: aws.String("sn-x"), Policy: aws.String("{")})
	_, cases["GetService"] = client.GetService(ctx, &awsvpcl.GetServiceInput{ServiceIdentifier: aws.String("svc-missing")})
	_, cases["ListServices"] = client.ListServices(ctx, &awsvpcl.ListServicesInput{NextToken: aws.String("%%bad")})

	for op, err := range cases {
		var generic *smithy.GenericAPIError
		if err == nil || errors.As(err, &generic) {
			t.Errorf("%s: want a modeled typed exception, got %v", op, err)
		}
	}
}

// --- pagination ---

func walkPages(t *testing.T, method, base string) (total, pages int) {
	t.Helper()

	token := ""

	for {
		u := base + "?maxResults=1"
		if token != "" {
			u += "&nextToken=" + url.QueryEscape(token)
		}

		req, _ := http.NewRequest(method, u, nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		var body struct {
			Items     []json.RawMessage `json:"items"`
			NextToken string            `json:"nextToken"`
		}

		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()

		if err != nil || resp.StatusCode != http.StatusOK || len(body.Items) > 1 {
			t.Fatalf("%s %s: status %d err %v items %d", method, base, resp.StatusCode, err, len(body.Items))
		}

		total += len(body.Items)
		pages++

		if body.NextToken == "" {
			return total, pages
		}

		token = body.NextToken

		if pages > 20 {
			t.Fatalf("%s: runaway pagination", base)
		}
	}
}

func TestPagination_AllListOps(t *testing.T) {
	_, cloud, base := newCloudClient(t)
	ctx := context.Background()
	d := cloud.VPCLattice

	vpcInfo, _ := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})

	var svcIDs, snIDs, rcIDs []string

	for _, n := range []string{"a", "b", "c"} {
		svc, _ := d.CreateService(ctx, &lattdriver.CreateServiceInput{Name: "svc-" + n})
		sn, _ := d.CreateServiceNetwork(ctx, &lattdriver.CreateServiceNetworkInput{Name: "sn-" + n})
		_, _ = d.CreateTargetGroup(ctx, &lattdriver.CreateTargetGroupInput{Name: "tg-" + n, Type: "IP"})
		gw, _ := d.CreateResourceGateway(ctx, &lattdriver.CreateResourceGatewayInput{Name: "gw-" + n, VpcID: vpcInfo.ID})
		rc, _ := d.CreateResourceConfiguration(ctx, &lattdriver.CreateResourceConfigurationInput{
			Name: "rc-" + n, Type: "SINGLE", ResourceGatewayID: gw.ID,
		})
		_, _ = d.CreateAccessLogSubscription(ctx, sn.ID, "arn:aws:s3:::b", "", nil)
		_, _ = d.StartDomainVerification(ctx, n+".example.com", nil)
		svcIDs, snIDs, rcIDs = append(svcIDs, svc.ID), append(snIDs, sn.ID), append(rcIDs, rc.ID)
	}

	for i := range svcIDs {
		_, _ = d.CreateSNServiceAssociation(ctx, snIDs[0], svcIDs[i], nil)
		_, _ = d.CreateSNResourceAssociation(ctx, snIDs[i], rcIDs[0], false, nil)
		_, _ = d.CreateSNVpcAssociation(ctx, &lattdriver.CreateSNVpcAssociationInput{ServiceNetworkID: snIDs[i], VpcID: vpcInfo.ID})
	}

	var lid string

	for _, p := range []int32{81, 82} {
		l, _ := d.CreateListener(ctx, &lattdriver.CreateListenerInput{ServiceID: svcIDs[0], Protocol: "HTTP", Port: p})
		lid = l.ID
	}

	for _, p := range []int32{1, 2} {
		_, _ = d.CreateRule(ctx, &lattdriver.CreateRuleInput{ServiceID: svcIDs[0], ListenerID: lid, Name: "r", Priority: p})
	}

	tgs, _ := d.ListTargetGroups(ctx)
	_, _, _ = d.RegisterTargets(ctx, tgs[0].ID, []lattdriver.RegisteredTarget{
		{ID: "10.0.0.1", Port: 80}, {ID: "10.0.0.2", Port: 80}, {ID: "10.0.0.3", Port: 80},
	})

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/services", 3},
		{http.MethodGet, "/servicenetworks", 3},
		{http.MethodGet, "/targetgroups", 3},
		{http.MethodGet, "/resourcegateways", 3},
		{http.MethodGet, "/resourceconfigurations", 3},
		{http.MethodGet, "/accesslogsubscriptions", 3},
		{http.MethodGet, "/domainverifications", 3},
		{http.MethodGet, "/servicenetworkserviceassociations", 3},
		{http.MethodGet, "/servicenetworkresourceassociations", 3},
		{http.MethodGet, "/servicenetworkvpcassociations", 3},
		{http.MethodGet, "/services/" + svcIDs[0] + "/listeners", 2},
		{http.MethodGet, "/services/" + svcIDs[0] + "/listeners/" + lid + "/rules", 3}, // default + 2
		{http.MethodPost, "/targetgroups/" + tgs[0].ID + "/listtargets", 3},
	}

	for _, c := range cases {
		total, pages := walkPages(t, c.method, base+c.path)
		if total != c.want || pages != c.want {
			t.Errorf("%s %s: total %d pages %d, want %d/%d", c.method, c.path, total, pages, c.want, c.want)
		}
	}
}

func TestPagination_Empty(t *testing.T) {
	client := newClient(t)

	out, err := client.ListServices(context.Background(), &awsvpcl.ListServicesInput{MaxResults: aws.Int32(5)})
	if err != nil || len(out.Items) != 0 || out.NextToken != nil {
		t.Fatalf("empty list: %v %+v", err, out)
	}
}

func TestPagination_Invalid(t *testing.T) {
	client, _, base := newCloudClient(t)
	ctx := context.Background()

	for _, n := range []int32{0, 101} {
		_, err := client.ListServices(ctx, &awsvpcl.ListServicesInput{MaxResults: aws.Int32(n)})
		requireAPIError[*vpcltypes.ValidationException](t, err, "maxResults")
	}

	_, err := client.ListServices(ctx, &awsvpcl.ListServicesInput{NextToken: aws.String("not-a-token")})
	requireAPIError[*vpcltypes.ValidationException](t, err, "nextToken")

	resp, err := http.Get(base + "/services?maxResults=abc")
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("maxResults=abc: status %d, want 400", resp.StatusCode)
	}
}

func TestOperationName(t *testing.T) {
	cases := []struct{ method, path, want string }{
		{http.MethodPost, "/servicenetworks", "CreateServiceNetwork"},
		{http.MethodGet, "/servicenetworks", "ListServiceNetworks"},
		{http.MethodGet, "/servicenetworks/sn-1", "GetServiceNetwork"},
		{http.MethodPatch, "/servicenetworks/sn-1", "UpdateServiceNetwork"},
		{http.MethodDelete, "/servicenetworks/sn-1", "DeleteServiceNetwork"},
		{http.MethodPost, "/services", "CreateService"},
		{http.MethodPost, "/services/svc-1/listeners", "CreateListener"},
		{http.MethodGet, "/services/svc-1/listeners", "ListListeners"},
		{http.MethodDelete, "/services/svc-1/listeners/listener-1", "DeleteListener"},
		{http.MethodPost, "/services/svc-1/listeners/listener-1/rules", "CreateRule"},
		{http.MethodPatch, "/services/svc-1/listeners/listener-1/rules", "BatchUpdateRule"},
		{http.MethodGet, "/services/svc-1/listeners/listener-1/rules/rule-1", "GetRule"},
		{http.MethodPost, "/targetgroups", "CreateTargetGroup"},
		{http.MethodPost, "/targetgroups/tg-1/registertargets", "RegisterTargets"},
		{http.MethodPost, "/targetgroups/tg-1/deregistertargets", "DeregisterTargets"},
		{http.MethodPost, "/targetgroups/tg-1/listtargets", "ListTargets"},
		{http.MethodPost, "/servicenetworkvpcassociations", "CreateServiceNetworkVpcAssociation"},
		{http.MethodGet, "/servicenetworkvpcendpointassociations", "ListServiceNetworkVpcEndpointAssociations"},
		{http.MethodDelete, "/servicenetworkserviceassociations/snsa-1", "DeleteServiceNetworkServiceAssociation"},
		{http.MethodGet, "/servicenetworkresourceassociations", "ListServiceNetworkResourceAssociations"},
		{http.MethodPost, "/resourceconfigurations", "CreateResourceConfiguration"},
		{http.MethodPatch, "/resourcegateways/rg-1", "UpdateResourceGateway"},
		{http.MethodDelete, "/resourceendpointassociations/rea-1", "DeleteResourceEndpointAssociation"},
		{http.MethodPost, "/accesslogsubscriptions", "CreateAccessLogSubscription"},
		{http.MethodPut, "/authpolicy/sn-1", "PutAuthPolicy"},
		{http.MethodGet, "/resourcepolicy/arn:aws:vpc-lattice:x", "GetResourcePolicy"},
		{http.MethodPost, "/domainverifications", "StartDomainVerification"},
		{http.MethodPost, "/tags/arn:aws:vpc-lattice:x", "TagResource"},
		{http.MethodGet, "/tags/arn:aws:vpc-lattice:x", "ListTagsForResource"},
		{http.MethodDelete, "/tags/arn:aws:vpc-lattice:x", "UntagResource"},
		{http.MethodGet, "/notalatticeroot", ""},
		{http.MethodPut, "/services", ""},
		{http.MethodGet, "/", ""},
	}

	for _, c := range cases {
		if got := vpclatticesrv.OperationName(c.method, c.path); got != c.want {
			t.Errorf("OperationName(%s %s) = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
