package route53resolver_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsr53r "github.com/aws/aws-sdk-go-v2/service/route53resolver"
	r53rtypes "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newClient builds an httptest-backed Route 53 Resolver client driving the real
// aws-sdk-go-v2 client against the in-memory driver.
func newClient(t *testing.T) *awsr53r.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Route53Resolver: cloud.Route53Resolver})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsr53r.NewFromConfig(cfg, func(o *awsr53r.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKResolverEndpointLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateResolverEndpoint(ctx, &awsr53r.CreateResolverEndpointInput{
		CreatorRequestId: aws.String("req-1"),
		Name:             aws.String("inbound-1"),
		Direction:        r53rtypes.ResolverEndpointDirectionInbound,
		SecurityGroupIds: []string{"sg-123"},
		IpAddresses: []r53rtypes.IpAddressRequest{
			{SubnetId: aws.String("subnet-1")},
			{SubnetId: aws.String("subnet-2")},
		},
	})
	if err != nil {
		t.Fatalf("CreateResolverEndpoint: %v", err)
	}

	ep := created.ResolverEndpoint
	if ep == nil || aws.ToString(ep.Id) == "" {
		t.Fatalf("no endpoint id: %+v", ep)
	}

	if ep.Direction != r53rtypes.ResolverEndpointDirectionInbound {
		t.Errorf("direction = %v, want INBOUND", ep.Direction)
	}

	if aws.ToInt32(ep.IpAddressCount) != 2 {
		t.Errorf("ip count = %d, want 2", aws.ToInt32(ep.IpAddressCount))
	}

	if ep.Status != r53rtypes.ResolverEndpointStatusOperational {
		t.Errorf("status = %v, want OPERATIONAL", ep.Status)
	}

	id := aws.ToString(ep.Id)
	arn := aws.ToString(ep.Arn)

	got, err := client.GetResolverEndpoint(ctx, &awsr53r.GetResolverEndpointInput{ResolverEndpointId: aws.String(id)})
	if err != nil || aws.ToString(got.ResolverEndpoint.Name) != "inbound-1" {
		t.Fatalf("GetResolverEndpoint: %v %+v", err, got)
	}

	list, err := client.ListResolverEndpoints(ctx, &awsr53r.ListResolverEndpointsInput{})
	if err != nil || len(list.ResolverEndpoints) != 1 {
		t.Fatalf("ListResolverEndpoints: %v %+v", err, list)
	}

	upd, err := client.UpdateResolverEndpoint(ctx, &awsr53r.UpdateResolverEndpointInput{
		ResolverEndpointId: aws.String(id),
		Name:               aws.String("renamed"),
	})
	if err != nil || aws.ToString(upd.ResolverEndpoint.Name) != "renamed" {
		t.Fatalf("UpdateResolverEndpoint: %v %+v", err, upd)
	}

	assoc, err := client.AssociateResolverEndpointIpAddress(ctx, &awsr53r.AssociateResolverEndpointIpAddressInput{
		ResolverEndpointId: aws.String(id),
		IpAddress:          &r53rtypes.IpAddressUpdate{SubnetId: aws.String("subnet-3")},
	})
	if err != nil || aws.ToInt32(assoc.ResolverEndpoint.IpAddressCount) != 3 {
		t.Fatalf("AssociateResolverEndpointIpAddress: %v %+v", err, assoc)
	}

	ips, err := client.ListResolverEndpointIpAddresses(ctx, &awsr53r.ListResolverEndpointIpAddressesInput{
		ResolverEndpointId: aws.String(id),
	})
	if err != nil || len(ips.IpAddresses) != 3 {
		t.Fatalf("ListResolverEndpointIpAddresses: %v %+v", err, ips)
	}

	dis, err := client.DisassociateResolverEndpointIpAddress(ctx, &awsr53r.DisassociateResolverEndpointIpAddressInput{
		ResolverEndpointId: aws.String(id),
		IpAddress:          &r53rtypes.IpAddressUpdate{IpId: ips.IpAddresses[0].IpId},
	})
	if err != nil || aws.ToInt32(dis.ResolverEndpoint.IpAddressCount) != 2 {
		t.Fatalf("DisassociateResolverEndpointIpAddress: %v %+v", err, dis)
	}

	if _, err := client.TagResource(ctx, &awsr53r.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []r53rtypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := client.ListTagsForResource(ctx, &awsr53r.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Value) != "test" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags)
	}

	del, err := client.DeleteResolverEndpoint(ctx, &awsr53r.DeleteResolverEndpointInput{ResolverEndpointId: aws.String(id)})
	if err != nil || del.ResolverEndpoint.Status != r53rtypes.ResolverEndpointStatusDeleting {
		t.Fatalf("DeleteResolverEndpoint: %v %+v", err, del)
	}

	_, err = client.GetResolverEndpoint(ctx, &awsr53r.GetResolverEndpointInput{ResolverEndpointId: aws.String(id)})

	var nfe *r53rtypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %v", err)
	}
}

func TestSDKResolverRuleLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateResolverRule(ctx, &awsr53r.CreateResolverRuleInput{
		CreatorRequestId:   aws.String("req-rule-1"),
		Name:               aws.String("fwd-example"),
		RuleType:           r53rtypes.RuleTypeOptionForward,
		DomainName:         aws.String("example.com"),
		ResolverEndpointId: aws.String("rslvr-out-abc"),
		TargetIps:          []r53rtypes.TargetAddress{{Ip: aws.String("10.0.0.2"), Port: aws.Int32(53)}},
	})
	if err != nil {
		t.Fatalf("CreateResolverRule: %v", err)
	}

	rule := created.ResolverRule
	if rule == nil || aws.ToString(rule.Id) == "" {
		t.Fatalf("no rule id: %+v", rule)
	}

	if rule.RuleType != r53rtypes.RuleTypeOptionForward {
		t.Errorf("ruleType = %v, want FORWARD", rule.RuleType)
	}

	if len(rule.TargetIps) != 1 || aws.ToString(rule.TargetIps[0].Ip) != "10.0.0.2" {
		t.Errorf("target ips = %+v", rule.TargetIps)
	}

	id := aws.ToString(rule.Id)
	arn := aws.ToString(rule.Arn)

	got, err := client.GetResolverRule(ctx, &awsr53r.GetResolverRuleInput{ResolverRuleId: aws.String(id)})
	if err != nil || aws.ToString(got.ResolverRule.DomainName) != "example.com" {
		t.Fatalf("GetResolverRule: %v %+v", err, got)
	}

	list, err := client.ListResolverRules(ctx, &awsr53r.ListResolverRulesInput{})
	if err != nil || len(list.ResolverRules) != 1 {
		t.Fatalf("ListResolverRules: %v %+v", err, list)
	}

	upd, err := client.UpdateResolverRule(ctx, &awsr53r.UpdateResolverRuleInput{
		ResolverRuleId: aws.String(id),
		Config: &r53rtypes.ResolverRuleConfig{
			Name:      aws.String("renamed-rule"),
			TargetIps: []r53rtypes.TargetAddress{{Ip: aws.String("10.0.0.3"), Port: aws.Int32(53)}},
		},
	})
	if err != nil || aws.ToString(upd.ResolverRule.Name) != "renamed-rule" {
		t.Fatalf("UpdateResolverRule: %v %+v", err, upd)
	}

	assoc, err := client.AssociateResolverRule(ctx, &awsr53r.AssociateResolverRuleInput{
		ResolverRuleId: aws.String(id),
		VPCId:          aws.String("vpc-123"),
		Name:           aws.String("assoc-1"),
	})
	if err != nil || assoc.ResolverRuleAssociation == nil {
		t.Fatalf("AssociateResolverRule: %v %+v", err, assoc)
	}

	assocID := aws.ToString(assoc.ResolverRuleAssociation.Id)

	ga, err := client.GetResolverRuleAssociation(ctx, &awsr53r.GetResolverRuleAssociationInput{
		ResolverRuleAssociationId: aws.String(assocID),
	})
	if err != nil || aws.ToString(ga.ResolverRuleAssociation.VPCId) != "vpc-123" {
		t.Fatalf("GetResolverRuleAssociation: %v %+v", err, ga)
	}

	la, err := client.ListResolverRuleAssociations(ctx, &awsr53r.ListResolverRuleAssociationsInput{})
	if err != nil || len(la.ResolverRuleAssociations) != 1 {
		t.Fatalf("ListResolverRuleAssociations: %v %+v", err, la)
	}

	if _, err := client.PutResolverRulePolicy(ctx, &awsr53r.PutResolverRulePolicyInput{
		Arn:                aws.String(arn),
		ResolverRulePolicy: aws.String(`{"policy":true}`),
	}); err != nil {
		t.Fatalf("PutResolverRulePolicy: %v", err)
	}

	pol, err := client.GetResolverRulePolicy(ctx, &awsr53r.GetResolverRulePolicyInput{Arn: aws.String(arn)})
	if err != nil || aws.ToString(pol.ResolverRulePolicy) != `{"policy":true}` {
		t.Fatalf("GetResolverRulePolicy: %v %+v", err, pol)
	}

	if _, err := client.DisassociateResolverRule(ctx, &awsr53r.DisassociateResolverRuleInput{
		ResolverRuleId: aws.String(id),
		VPCId:          aws.String("vpc-123"),
	}); err != nil {
		t.Fatalf("DisassociateResolverRule: %v", err)
	}

	if _, err := client.DeleteResolverRule(ctx, &awsr53r.DeleteResolverRuleInput{ResolverRuleId: aws.String(id)}); err != nil {
		t.Fatalf("DeleteResolverRule: %v", err)
	}

	_, err = client.GetResolverRule(ctx, &awsr53r.GetResolverRuleInput{ResolverRuleId: aws.String(id)})

	var rnfe *r53rtypes.ResourceNotFoundException
	if !errors.As(err, &rnfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %v", err)
	}
}

func TestSDKQueryLogConfigLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateResolverQueryLogConfig(ctx, &awsr53r.CreateResolverQueryLogConfigInput{
		CreatorRequestId: aws.String("req-qlc-1"),
		Name:             aws.String("qlc-1"),
		DestinationArn:   aws.String("arn:aws:s3:::my-logs"),
	})
	if err != nil {
		t.Fatalf("CreateResolverQueryLogConfig: %v", err)
	}

	cfg := created.ResolverQueryLogConfig
	if cfg == nil || aws.ToString(cfg.Id) == "" {
		t.Fatalf("no config id: %+v", cfg)
	}

	id := aws.ToString(cfg.Id)
	arn := aws.ToString(cfg.Arn)

	list, err := client.ListResolverQueryLogConfigs(ctx, &awsr53r.ListResolverQueryLogConfigsInput{})
	if err != nil || len(list.ResolverQueryLogConfigs) != 1 {
		t.Fatalf("ListResolverQueryLogConfigs: %v %+v", err, list)
	}

	assoc, err := client.AssociateResolverQueryLogConfig(ctx, &awsr53r.AssociateResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: aws.String(id),
		ResourceId:               aws.String("vpc-abc"),
	})
	if err != nil || assoc.ResolverQueryLogConfigAssociation == nil {
		t.Fatalf("AssociateResolverQueryLogConfig: %v %+v", err, assoc)
	}

	assocID := aws.ToString(assoc.ResolverQueryLogConfigAssociation.Id)

	ga, err := client.GetResolverQueryLogConfigAssociation(ctx, &awsr53r.GetResolverQueryLogConfigAssociationInput{
		ResolverQueryLogConfigAssociationId: aws.String(assocID),
	})
	if err != nil || aws.ToString(ga.ResolverQueryLogConfigAssociation.ResourceId) != "vpc-abc" {
		t.Fatalf("GetResolverQueryLogConfigAssociation: %v %+v", err, ga)
	}

	la, err := client.ListResolverQueryLogConfigAssociations(ctx, &awsr53r.ListResolverQueryLogConfigAssociationsInput{})
	if err != nil || len(la.ResolverQueryLogConfigAssociations) != 1 {
		t.Fatalf("ListResolverQueryLogConfigAssociations: %v %+v", err, la)
	}

	gc, err := client.GetResolverQueryLogConfig(ctx, &awsr53r.GetResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: aws.String(id),
	})
	if err != nil || gc.ResolverQueryLogConfig.AssociationCount != 1 {
		t.Fatalf("GetResolverQueryLogConfig assoc count: %v %+v", err, gc)
	}

	if _, err := client.PutResolverQueryLogConfigPolicy(ctx, &awsr53r.PutResolverQueryLogConfigPolicyInput{
		Arn:                          aws.String(arn),
		ResolverQueryLogConfigPolicy: aws.String(`{"p":1}`),
	}); err != nil {
		t.Fatalf("PutResolverQueryLogConfigPolicy: %v", err)
	}

	pol, err := client.GetResolverQueryLogConfigPolicy(ctx, &awsr53r.GetResolverQueryLogConfigPolicyInput{Arn: aws.String(arn)})
	if err != nil || aws.ToString(pol.ResolverQueryLogConfigPolicy) != `{"p":1}` {
		t.Fatalf("GetResolverQueryLogConfigPolicy: %v %+v", err, pol)
	}

	if _, err := client.DisassociateResolverQueryLogConfig(ctx, &awsr53r.DisassociateResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: aws.String(id),
		ResourceId:               aws.String("vpc-abc"),
	}); err != nil {
		t.Fatalf("DisassociateResolverQueryLogConfig: %v", err)
	}

	if _, err := client.DeleteResolverQueryLogConfig(ctx, &awsr53r.DeleteResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: aws.String(id),
	}); err != nil {
		t.Fatalf("DeleteResolverQueryLogConfig: %v", err)
	}

	_, err = client.GetResolverQueryLogConfig(ctx, &awsr53r.GetResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: aws.String(id),
	})

	var qnfe *r53rtypes.ResourceNotFoundException
	if !errors.As(err, &qnfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %v", err)
	}
}

func TestSDKResolverConfigLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	got, err := client.GetResolverConfig(ctx, &awsr53r.GetResolverConfigInput{
		ResourceId: aws.String("vpc-cfg-1"),
	})
	if err != nil || got.ResolverConfig.AutodefinedReverse != r53rtypes.ResolverAutodefinedReverseStatusEnabled {
		t.Fatalf("GetResolverConfig default: %v %+v", err, got.ResolverConfig)
	}

	upd, err := client.UpdateResolverConfig(ctx, &awsr53r.UpdateResolverConfigInput{
		ResourceId:             aws.String("vpc-cfg-1"),
		AutodefinedReverseFlag: r53rtypes.AutodefinedReverseFlagDisable,
	})
	if err != nil || upd.ResolverConfig.AutodefinedReverse != r53rtypes.ResolverAutodefinedReverseStatusDisabled {
		t.Fatalf("UpdateResolverConfig: %v %+v", err, upd.ResolverConfig)
	}

	list, err := client.ListResolverConfigs(ctx, &awsr53r.ListResolverConfigsInput{})
	if err != nil || len(list.ResolverConfigs) != 1 || aws.ToString(list.ResolverConfigs[0].ResourceId) != "vpc-cfg-1" {
		t.Fatalf("ListResolverConfigs: %v %+v", err, list.ResolverConfigs)
	}
}

func TestSDKResolverDnssecConfigLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	got, err := client.GetResolverDnssecConfig(ctx, &awsr53r.GetResolverDnssecConfigInput{
		ResourceId: aws.String("vpc-ds-1"),
	})
	if err != nil || got.ResolverDNSSECConfig.ValidationStatus != r53rtypes.ResolverDNSSECValidationStatusDisabled {
		t.Fatalf("GetResolverDnssecConfig default: %v %+v", err, got.ResolverDNSSECConfig)
	}

	upd, err := client.UpdateResolverDnssecConfig(ctx, &awsr53r.UpdateResolverDnssecConfigInput{
		ResourceId: aws.String("vpc-ds-1"),
		Validation: r53rtypes.ValidationEnable,
	})
	if err != nil || upd.ResolverDNSSECConfig.ValidationStatus != r53rtypes.ResolverDNSSECValidationStatusEnabled {
		t.Fatalf("UpdateResolverDnssecConfig: %v %+v", err, upd.ResolverDNSSECConfig)
	}

	list, err := client.ListResolverDnssecConfigs(ctx, &awsr53r.ListResolverDnssecConfigsInput{})
	if err != nil || len(list.ResolverDnssecConfigs) != 1 {
		t.Fatalf("ListResolverDnssecConfigs: %v %+v", err, list.ResolverDnssecConfigs)
	}
}

func TestSDKFirewallLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	dl, err := client.CreateFirewallDomainList(ctx, &awsr53r.CreateFirewallDomainListInput{
		CreatorRequestId: aws.String("dl-req"),
		Name:             aws.String("blocklist"),
	})
	if err != nil {
		t.Fatalf("CreateFirewallDomainList: %v", err)
	}

	dlID := aws.ToString(dl.FirewallDomainList.Id)

	if _, err = client.UpdateFirewallDomains(ctx, &awsr53r.UpdateFirewallDomainsInput{
		FirewallDomainListId: aws.String(dlID),
		Operation:            r53rtypes.FirewallDomainUpdateOperationAdd,
		Domains:              []string{"evil.example.com", "bad.example.net"},
	}); err != nil {
		t.Fatalf("UpdateFirewallDomains: %v", err)
	}

	ld, err := client.ListFirewallDomains(ctx, &awsr53r.ListFirewallDomainsInput{
		FirewallDomainListId: aws.String(dlID),
	})
	if err != nil || len(ld.Domains) != 2 {
		t.Fatalf("ListFirewallDomains: %v %+v", err, ld.Domains)
	}

	rg, err := client.CreateFirewallRuleGroup(ctx, &awsr53r.CreateFirewallRuleGroupInput{
		CreatorRequestId: aws.String("rg-req"),
		Name:             aws.String("rg-1"),
	})
	if err != nil {
		t.Fatalf("CreateFirewallRuleGroup: %v", err)
	}

	rgID := aws.ToString(rg.FirewallRuleGroup.Id)

	rule, err := client.CreateFirewallRule(ctx, &awsr53r.CreateFirewallRuleInput{
		FirewallRuleGroupId:  aws.String(rgID),
		FirewallDomainListId: aws.String(dlID),
		Name:                 aws.String("block-evil"),
		Priority:             aws.Int32(100),
		Action:               r53rtypes.ActionBlock,
		BlockResponse:        r53rtypes.BlockResponseNxdomain,
	})
	if err != nil || rule.FirewallRule.Action != r53rtypes.ActionBlock {
		t.Fatalf("CreateFirewallRule: %v %+v", err, rule.FirewallRule)
	}

	lr, err := client.ListFirewallRules(ctx, &awsr53r.ListFirewallRulesInput{
		FirewallRuleGroupId: aws.String(rgID),
	})
	if err != nil || len(lr.FirewallRules) != 1 {
		t.Fatalf("ListFirewallRules: %v %+v", err, lr.FirewallRules)
	}

	gr, err := client.GetFirewallRuleGroup(ctx, &awsr53r.GetFirewallRuleGroupInput{
		FirewallRuleGroupId: aws.String(rgID),
	})
	if err != nil || aws.ToInt32(gr.FirewallRuleGroup.RuleCount) != 1 {
		t.Fatalf("GetFirewallRuleGroup RuleCount: %v %+v", err, gr.FirewallRuleGroup)
	}

	assoc, err := client.AssociateFirewallRuleGroup(ctx, &awsr53r.AssociateFirewallRuleGroupInput{
		CreatorRequestId:    aws.String("assoc-req"),
		FirewallRuleGroupId: aws.String(rgID),
		Name:                aws.String("assoc-1"),
		Priority:            aws.Int32(101),
		VpcId:               aws.String("vpc-fw-1"),
	})
	if err != nil {
		t.Fatalf("AssociateFirewallRuleGroup: %v", err)
	}

	assocID := aws.ToString(assoc.FirewallRuleGroupAssociation.Id)

	if _, err = client.GetFirewallRuleGroupAssociation(ctx, &awsr53r.GetFirewallRuleGroupAssociationInput{
		FirewallRuleGroupAssociationId: aws.String(assocID),
	}); err != nil {
		t.Fatalf("GetFirewallRuleGroupAssociation: %v", err)
	}

	fc, err := client.GetFirewallConfig(ctx, &awsr53r.GetFirewallConfigInput{
		ResourceId: aws.String("vpc-fw-1"),
	})
	if err != nil || fc.FirewallConfig.FirewallFailOpen != r53rtypes.FirewallFailOpenStatusDisabled {
		t.Fatalf("GetFirewallConfig default: %v %+v", err, fc.FirewallConfig)
	}

	uc, err := client.UpdateFirewallConfig(ctx, &awsr53r.UpdateFirewallConfigInput{
		ResourceId:       aws.String("vpc-fw-1"),
		FirewallFailOpen: r53rtypes.FirewallFailOpenStatusEnabled,
	})
	if err != nil || uc.FirewallConfig.FirewallFailOpen != r53rtypes.FirewallFailOpenStatusEnabled {
		t.Fatalf("UpdateFirewallConfig: %v %+v", err, uc.FirewallConfig)
	}

	if _, err = client.DisassociateFirewallRuleGroup(ctx, &awsr53r.DisassociateFirewallRuleGroupInput{
		FirewallRuleGroupAssociationId: aws.String(assocID),
	}); err != nil {
		t.Fatalf("DisassociateFirewallRuleGroup: %v", err)
	}

	if _, err = client.DeleteFirewallRule(ctx, &awsr53r.DeleteFirewallRuleInput{
		FirewallRuleGroupId:  aws.String(rgID),
		FirewallDomainListId: aws.String(dlID),
	}); err != nil {
		t.Fatalf("DeleteFirewallRule: %v", err)
	}
}

func TestSDKOutpostResolverLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	created, err := client.CreateOutpostResolver(ctx, &awsr53r.CreateOutpostResolverInput{
		CreatorRequestId:      aws.String("op-req"),
		Name:                  aws.String("op-1"),
		OutpostArn:            aws.String("arn:aws:outposts:us-east-1:123:outpost/op-abc"),
		PreferredInstanceType: aws.String("m5.large"),
		InstanceCount:         aws.Int32(4),
	})
	if err != nil || aws.ToInt32(created.OutpostResolver.InstanceCount) != 4 {
		t.Fatalf("CreateOutpostResolver: %v %+v", err, created.OutpostResolver)
	}

	id := aws.ToString(created.OutpostResolver.Id)

	upd, err := client.UpdateOutpostResolver(ctx, &awsr53r.UpdateOutpostResolverInput{
		Id:            aws.String(id),
		InstanceCount: aws.Int32(8),
	})
	if err != nil || aws.ToInt32(upd.OutpostResolver.InstanceCount) != 8 {
		t.Fatalf("UpdateOutpostResolver: %v %+v", err, upd.OutpostResolver)
	}

	got, err := client.GetOutpostResolver(ctx, &awsr53r.GetOutpostResolverInput{Id: aws.String(id)})
	if err != nil || aws.ToString(got.OutpostResolver.Name) != "op-1" {
		t.Fatalf("GetOutpostResolver: %v %+v", err, got.OutpostResolver)
	}

	list, err := client.ListOutpostResolvers(ctx, &awsr53r.ListOutpostResolversInput{})
	if err != nil || len(list.OutpostResolvers) != 1 {
		t.Fatalf("ListOutpostResolvers: %v %+v", err, list.OutpostResolvers)
	}

	if _, err = client.DeleteOutpostResolver(ctx, &awsr53r.DeleteOutpostResolverInput{Id: aws.String(id)}); err != nil {
		t.Fatalf("DeleteOutpostResolver: %v", err)
	}

	if _, err = client.GetOutpostResolver(ctx, &awsr53r.GetOutpostResolverInput{Id: aws.String(id)}); err == nil {
		t.Fatal("GetOutpostResolver after delete: expected error, got nil")
	}
}

// TestSDKFirewallFullSurface drives the firewall handlers not exercised by the
// happy-path lifecycle: batch rule ops, policies, list variants, import, and
// association update.
func TestSDKFirewallFullSurface(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	dl, err := client.CreateFirewallDomainList(ctx, &awsr53r.CreateFirewallDomainListInput{
		CreatorRequestId: aws.String("dl"), Name: aws.String("dl-1"),
	})
	require(t, err, "CreateFirewallDomainList")
	dlID := aws.ToString(dl.FirewallDomainList.Id)

	if _, err = client.GetFirewallDomainList(ctx, &awsr53r.GetFirewallDomainListInput{
		FirewallDomainListId: aws.String(dlID),
	}); err != nil {
		t.Fatalf("GetFirewallDomainList: %v", err)
	}

	if _, err = client.ImportFirewallDomains(ctx, &awsr53r.ImportFirewallDomainsInput{
		FirewallDomainListId: aws.String(dlID),
		Operation:            r53rtypes.FirewallDomainImportOperationReplace,
		DomainFileUrl:        aws.String("s3://bucket/domains.txt"),
	}); err != nil {
		t.Fatalf("ImportFirewallDomains: %v", err)
	}

	ldl, err := client.ListFirewallDomainLists(ctx, &awsr53r.ListFirewallDomainListsInput{})
	if err != nil || len(ldl.FirewallDomainLists) != 1 {
		t.Fatalf("ListFirewallDomainLists: %v %+v", err, ldl.FirewallDomainLists)
	}

	rg, err := client.CreateFirewallRuleGroup(ctx, &awsr53r.CreateFirewallRuleGroupInput{
		CreatorRequestId: aws.String("rg"), Name: aws.String("rg-1"),
	})
	require(t, err, "CreateFirewallRuleGroup")
	rgID := aws.ToString(rg.FirewallRuleGroup.Id)

	bc, err := client.BatchCreateFirewallRule(ctx, &awsr53r.BatchCreateFirewallRuleInput{
		CreateFirewallRuleEntries: []r53rtypes.CreateFirewallRuleEntry{{
			CreatorRequestId:     aws.String("bc-1"),
			FirewallRuleGroupId:  aws.String(rgID),
			FirewallDomainListId: aws.String(dlID),
			Name:                 aws.String("r1"),
			Priority:             aws.Int32(10),
			Action:               r53rtypes.ActionBlock,
			BlockResponse:        r53rtypes.BlockResponseNxdomain,
		}},
	})
	require(t, err, "BatchCreateFirewallRule")
	if len(bc.CreatedFirewallRules) != 1 {
		t.Fatalf("BatchCreateFirewallRule count: %+v", bc.CreatedFirewallRules)
	}

	if _, err = client.UpdateFirewallRule(ctx, &awsr53r.UpdateFirewallRuleInput{
		FirewallRuleGroupId:  aws.String(rgID),
		FirewallDomainListId: aws.String(dlID),
		Action:               r53rtypes.ActionAlert,
		Priority:             aws.Int32(10),
	}); err != nil {
		t.Fatalf("UpdateFirewallRule: %v", err)
	}

	bu, err := client.BatchUpdateFirewallRule(ctx, &awsr53r.BatchUpdateFirewallRuleInput{
		UpdateFirewallRuleEntries: []r53rtypes.UpdateFirewallRuleEntry{{
			FirewallRuleGroupId:  aws.String(rgID),
			FirewallDomainListId: aws.String(dlID),
			Action:               r53rtypes.ActionAllow,
			Priority:             aws.Int32(10),
		}},
	})
	if err != nil || len(bu.UpdatedFirewallRules) != 1 {
		t.Fatalf("BatchUpdateFirewallRule: %v %+v", err, bu.UpdatedFirewallRules)
	}

	if _, err = client.PutFirewallRuleGroupPolicy(ctx, &awsr53r.PutFirewallRuleGroupPolicyInput{
		Arn: aws.String(aws.ToString(rg.FirewallRuleGroup.Arn)), FirewallRuleGroupPolicy: aws.String("{}"),
	}); err != nil {
		t.Fatalf("PutFirewallRuleGroupPolicy: %v", err)
	}
	gp, err := client.GetFirewallRuleGroupPolicy(ctx, &awsr53r.GetFirewallRuleGroupPolicyInput{
		Arn: aws.String(aws.ToString(rg.FirewallRuleGroup.Arn)),
	})
	if err != nil || aws.ToString(gp.FirewallRuleGroupPolicy) != "{}" {
		t.Fatalf("GetFirewallRuleGroupPolicy: %v %+v", err, gp)
	}

	assoc, err := client.AssociateFirewallRuleGroup(ctx, &awsr53r.AssociateFirewallRuleGroupInput{
		CreatorRequestId: aws.String("a"), FirewallRuleGroupId: aws.String(rgID),
		Name: aws.String("a1"), Priority: aws.Int32(101), VpcId: aws.String("vpc-1"),
	})
	require(t, err, "AssociateFirewallRuleGroup")
	assocID := aws.ToString(assoc.FirewallRuleGroupAssociation.Id)

	if _, err = client.UpdateFirewallRuleGroupAssociation(ctx, &awsr53r.UpdateFirewallRuleGroupAssociationInput{
		FirewallRuleGroupAssociationId: aws.String(assocID),
		Name:                           aws.String("a2"),
		Priority:                       aws.Int32(202),
	}); err != nil {
		t.Fatalf("UpdateFirewallRuleGroupAssociation: %v", err)
	}

	la, err := client.ListFirewallRuleGroupAssociations(ctx, &awsr53r.ListFirewallRuleGroupAssociationsInput{})
	if err != nil || len(la.FirewallRuleGroupAssociations) != 1 {
		t.Fatalf("ListFirewallRuleGroupAssociations: %v %+v", err, la.FirewallRuleGroupAssociations)
	}

	lrg, err := client.ListFirewallRuleGroups(ctx, &awsr53r.ListFirewallRuleGroupsInput{})
	if err != nil || len(lrg.FirewallRuleGroups) != 1 {
		t.Fatalf("ListFirewallRuleGroups: %v %+v", err, lrg.FirewallRuleGroups)
	}

	if _, err = client.ListFirewallConfigs(ctx, &awsr53r.ListFirewallConfigsInput{}); err != nil {
		t.Fatalf("ListFirewallConfigs: %v", err)
	}
	if _, err = client.ListFirewallRuleTypes(ctx, &awsr53r.ListFirewallRuleTypesInput{}); err != nil {
		t.Fatalf("ListFirewallRuleTypes: %v", err)
	}

	bd, err := client.BatchDeleteFirewallRule(ctx, &awsr53r.BatchDeleteFirewallRuleInput{
		DeleteFirewallRuleEntries: []r53rtypes.DeleteFirewallRuleEntry{{
			FirewallRuleGroupId:  aws.String(rgID),
			FirewallDomainListId: aws.String(dlID),
		}},
	})
	if err != nil || len(bd.DeletedFirewallRules) != 1 {
		t.Fatalf("BatchDeleteFirewallRule: %v %+v", err, bd.DeletedFirewallRules)
	}

	// A rule group with live VPC associations cannot be deleted — disassociate
	// first, matching real AWS.
	if _, err = client.DisassociateFirewallRuleGroup(ctx, &awsr53r.DisassociateFirewallRuleGroupInput{
		FirewallRuleGroupAssociationId: aws.String(assocID),
	}); err != nil {
		t.Fatalf("DisassociateFirewallRuleGroup: %v", err)
	}

	if _, err = client.DeleteFirewallRuleGroup(ctx, &awsr53r.DeleteFirewallRuleGroupInput{
		FirewallRuleGroupId: aws.String(rgID),
	}); err != nil {
		t.Fatalf("DeleteFirewallRuleGroup: %v", err)
	}
	if _, err = client.DeleteFirewallDomainList(ctx, &awsr53r.DeleteFirewallDomainListInput{
		FirewallDomainListId: aws.String(dlID),
	}); err != nil {
		t.Fatalf("DeleteFirewallDomainList: %v", err)
	}

	// Error mapping: a missing rule group surfaces as ResourceNotFoundException.
	_, err = client.GetFirewallRuleGroup(ctx, &awsr53r.GetFirewallRuleGroupInput{
		FirewallRuleGroupId: aws.String("rslvr-frg-missing"),
	})
	var nfe *r53rtypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

// TestSDKTaggingAndPolicies covers the tagging handlers and resolver/qlc policies.
func TestSDKTaggingAndPolicies(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	rule, err := client.CreateResolverRule(ctx, &awsr53r.CreateResolverRuleInput{
		CreatorRequestId: aws.String("r"), Name: aws.String("rule"),
		RuleType: r53rtypes.RuleTypeOptionForward, DomainName: aws.String("example.com"),
		ResolverEndpointId: aws.String("rslvr-out-1"),
		TargetIps:          []r53rtypes.TargetAddress{{Ip: aws.String("10.0.0.2")}},
	})
	require(t, err, "CreateResolverRule")
	arn := aws.ToString(rule.ResolverRule.Arn)

	if _, err = client.TagResource(ctx, &awsr53r.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []r53rtypes.Tag{{Key: aws.String("team"), Value: aws.String("net")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := client.ListTagsForResource(ctx, &awsr53r.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil || len(lt.Tags) != 1 {
		t.Fatalf("ListTagsForResource: %v %+v", err, lt.Tags)
	}

	if _, err = client.UntagResource(ctx, &awsr53r.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	if _, err = client.PutResolverRulePolicy(ctx, &awsr53r.PutResolverRulePolicyInput{
		Arn: aws.String(arn), ResolverRulePolicy: aws.String("{}"),
	}); err != nil {
		t.Fatalf("PutResolverRulePolicy: %v", err)
	}
	if _, err = client.GetResolverRulePolicy(ctx, &awsr53r.GetResolverRulePolicyInput{Arn: aws.String(arn)}); err != nil {
		t.Fatalf("GetResolverRulePolicy: %v", err)
	}
}

func require(t *testing.T, err error, op string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}
