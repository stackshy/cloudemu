package elbv2

import (
	"encoding/xml"
	"time"

	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// defaultAvailabilityZone is the placeholder AZ name reported for a load
// balancer's subnets; the emulator does not model subnet placement.
const defaultAvailabilityZone = "us-east-1a"

// All ELBv2 query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. Lists are wrapped in a
// <member> element per entry. The structures below mirror the AWS-published XML
// closely enough that aws-sdk-go-v2's elasticloadbalancingv2 unmarshalers
// consume them (the SDK matches element names case-insensitively).

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

// emptyResult is the payload for operations that return no data. The SDK still
// looks up the <XxxResult> element, so every response must carry one even when
// it's empty.
type emptyResult struct{}

// --- load balancer ---

type loadBalancerStateXML struct {
	Code   string `xml:"Code,omitempty"`
	Reason string `xml:"Reason,omitempty"`
}

type availabilityZoneXML struct {
	ZoneName string `xml:"ZoneName,omitempty"`
	SubnetID string `xml:"SubnetId,omitempty"`
}

type availabilityZonesXML struct {
	Member []availabilityZoneXML `xml:"member,omitempty"`
}

type securityGroupsXML struct {
	Member []string `xml:"member,omitempty"`
}

type loadBalancerXML struct {
	LoadBalancerArn       string                `xml:"LoadBalancerArn"`
	LoadBalancerName      string                `xml:"LoadBalancerName"`
	DNSName               string                `xml:"DNSName,omitempty"`
	CanonicalHostedZoneId string                `xml:"CanonicalHostedZoneId,omitempty"`
	CreatedTime           string                `xml:"CreatedTime,omitempty"`
	Scheme                string                `xml:"Scheme,omitempty"`
	Type                  string                `xml:"Type,omitempty"`
	VpcID                 string                `xml:"VpcId,omitempty"`
	IpAddressType         string                `xml:"IpAddressType,omitempty"`
	State                 *loadBalancerStateXML `xml:"State,omitempty"`
	AvailabilityZones     *availabilityZonesXML `xml:"AvailabilityZones,omitempty"`
	SecurityGroups        *securityGroupsXML    `xml:"SecurityGroups,omitempty"`
}

type loadBalancersXML struct {
	Member []loadBalancerXML `xml:"member,omitempty"`
}

type loadBalancersResult struct {
	LoadBalancers loadBalancersXML `xml:"LoadBalancers"`
	NextMarker    string           `xml:"NextMarker,omitempty"`
}

type createLoadBalancerResponse struct {
	XMLName  xml.Name            `xml:"CreateLoadBalancerResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   loadBalancersResult `xml:"CreateLoadBalancerResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type describeLoadBalancersResponse struct {
	XMLName  xml.Name            `xml:"DescribeLoadBalancersResponse"`
	Xmlns    string              `xml:"xmlns,attr"`
	Result   loadBalancersResult `xml:"DescribeLoadBalancersResult"`
	Metadata responseMetadata    `xml:"ResponseMetadata"`
}

type deleteLoadBalancerResponse struct {
	XMLName  xml.Name         `xml:"DeleteLoadBalancerResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"DeleteLoadBalancerResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- target group ---

type targetGroupXML struct {
	TargetGroupArn             string      `xml:"TargetGroupArn"`
	TargetGroupName            string      `xml:"TargetGroupName"`
	Protocol                   string      `xml:"Protocol,omitempty"`
	Port                       int         `xml:"Port,omitempty"`
	VpcID                      string      `xml:"VpcId,omitempty"`
	TargetType                 string      `xml:"TargetType,omitempty"`
	HealthCheckProtocol        string      `xml:"HealthCheckProtocol,omitempty"`
	HealthCheckPort            string      `xml:"HealthCheckPort,omitempty"`
	HealthCheckPath            string      `xml:"HealthCheckPath,omitempty"`
	HealthCheckEnabled         bool        `xml:"HealthCheckEnabled"`
	HealthCheckIntervalSeconds int         `xml:"HealthCheckIntervalSeconds,omitempty"`
	HealthCheckTimeoutSeconds  int         `xml:"HealthCheckTimeoutSeconds,omitempty"`
	HealthyThresholdCount      int         `xml:"HealthyThresholdCount,omitempty"`
	UnhealthyThresholdCount    int         `xml:"UnhealthyThresholdCount,omitempty"`
	Matcher                    *matcherXML `xml:"Matcher,omitempty"`
}

// matcherXML is the ELBv2 Matcher element (HTTP/gRPC success codes).
type matcherXML struct {
	HttpCode string `xml:"HttpCode,omitempty"`
}

type targetGroupsXML struct {
	Member []targetGroupXML `xml:"member,omitempty"`
}

type targetGroupsResult struct {
	TargetGroups targetGroupsXML `xml:"TargetGroups"`
	NextMarker   string          `xml:"NextMarker,omitempty"`
}

type createTargetGroupResponse struct {
	XMLName  xml.Name           `xml:"CreateTargetGroupResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   targetGroupsResult `xml:"CreateTargetGroupResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type describeTargetGroupsResponse struct {
	XMLName  xml.Name           `xml:"DescribeTargetGroupsResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   targetGroupsResult `xml:"DescribeTargetGroupsResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type deleteTargetGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteTargetGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"DeleteTargetGroupResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- listener / actions ---

type redirectConfigXML struct {
	Protocol   string `xml:"Protocol,omitempty"`
	Port       string `xml:"Port,omitempty"`
	Host       string `xml:"Host,omitempty"`
	Path       string `xml:"Path,omitempty"`
	Query      string `xml:"Query,omitempty"`
	StatusCode string `xml:"StatusCode,omitempty"`
}

type fixedResponseConfigXML struct {
	StatusCode  string `xml:"StatusCode,omitempty"`
	ContentType string `xml:"ContentType,omitempty"`
	MessageBody string `xml:"MessageBody,omitempty"`
}

type actionXML struct {
	Type                string                  `xml:"Type"`
	TargetGroupArn      string                  `xml:"TargetGroupArn,omitempty"`
	Order               int                     `xml:"Order,omitempty"`
	RedirectConfig      *redirectConfigXML      `xml:"RedirectConfig,omitempty"`
	FixedResponseConfig *fixedResponseConfigXML `xml:"FixedResponseConfig,omitempty"`
}

type actionsXML struct {
	Member []actionXML `xml:"member,omitempty"`
}

type certificateXML struct {
	CertificateArn string `xml:"CertificateArn,omitempty"`
	IsDefault      bool   `xml:"IsDefault,omitempty"`
}

type certificatesXML struct {
	Member []certificateXML `xml:"member,omitempty"`
}

type listenerXML struct {
	ListenerArn     string           `xml:"ListenerArn"`
	LoadBalancerArn string           `xml:"LoadBalancerArn,omitempty"`
	Protocol        string           `xml:"Protocol,omitempty"`
	Port            int              `xml:"Port,omitempty"`
	SslPolicy       string           `xml:"SslPolicy,omitempty"`
	Certificates    *certificatesXML `xml:"Certificates,omitempty"`
	DefaultActions  *actionsXML      `xml:"DefaultActions,omitempty"`
}

type listenersXML struct {
	Member []listenerXML `xml:"member,omitempty"`
}

type listenersResult struct {
	Listeners  listenersXML `xml:"Listeners"`
	NextMarker string       `xml:"NextMarker,omitempty"`
}

type createListenerResponse struct {
	XMLName  xml.Name         `xml:"CreateListenerResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listenersResult  `xml:"CreateListenerResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeListenersResponse struct {
	XMLName  xml.Name         `xml:"DescribeListenersResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listenersResult  `xml:"DescribeListenersResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteListenerResponse struct {
	XMLName  xml.Name         `xml:"DeleteListenerResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"DeleteListenerResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- rules ---

// valuesConfigXML is the shared shape of the condition configs that carry only
// a Values list (host-header, path-pattern, source-ip, http-request-method).
type valuesConfigXML struct {
	Values *stringListXML `xml:"Values,omitempty"`
}

type httpHeaderConfigXML struct {
	HTTPHeaderName string         `xml:"HttpHeaderName,omitempty"`
	Values         *stringListXML `xml:"Values,omitempty"`
}

type queryStringKVXML struct {
	Key   string `xml:"Key,omitempty"`
	Value string `xml:"Value,omitempty"`
}

type queryStringValuesXML struct {
	Member []queryStringKVXML `xml:"member,omitempty"`
}

type queryStringConfigXML struct {
	Values *queryStringValuesXML `xml:"Values,omitempty"`
}

type ruleConditionXML struct {
	Field                   string                `xml:"Field,omitempty"`
	Values                  *stringListXML        `xml:"Values,omitempty"`
	HostHeaderConfig        *valuesConfigXML      `xml:"HostHeaderConfig,omitempty"`
	PathPatternConfig       *valuesConfigXML      `xml:"PathPatternConfig,omitempty"`
	HTTPHeaderConfig        *httpHeaderConfigXML  `xml:"HttpHeaderConfig,omitempty"`
	QueryStringConfig       *queryStringConfigXML `xml:"QueryStringConfig,omitempty"`
	SourceIPConfig          *valuesConfigXML      `xml:"SourceIpConfig,omitempty"`
	HTTPRequestMethodConfig *valuesConfigXML      `xml:"HttpRequestMethodConfig,omitempty"`
}

type ruleConditionsXML struct {
	Member []ruleConditionXML `xml:"member,omitempty"`
}

type stringListXML struct {
	Member []string `xml:"member,omitempty"`
}

type ruleXML struct {
	RuleArn    string             `xml:"RuleArn"`
	Priority   string             `xml:"Priority,omitempty"`
	Conditions *ruleConditionsXML `xml:"Conditions,omitempty"`
	Actions    *actionsXML        `xml:"Actions,omitempty"`
	IsDefault  bool               `xml:"IsDefault"`
}

type rulesXML struct {
	Member []ruleXML `xml:"member,omitempty"`
}

type rulesResult struct {
	Rules      rulesXML `xml:"Rules"`
	NextMarker string   `xml:"NextMarker,omitempty"`
}

type createRuleResponse struct {
	XMLName  xml.Name         `xml:"CreateRuleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   rulesResult      `xml:"CreateRuleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeRulesResponse struct {
	XMLName  xml.Name         `xml:"DescribeRulesResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   rulesResult      `xml:"DescribeRulesResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deleteRuleResponse struct {
	XMLName  xml.Name         `xml:"DeleteRuleResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"DeleteRuleResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- targets / health ---

type targetDescriptionXML struct {
	ID   string `xml:"Id"`
	Port int    `xml:"Port,omitempty"`
}

type targetHealthXML struct {
	State       string `xml:"State,omitempty"`
	Reason      string `xml:"Reason,omitempty"`
	Description string `xml:"Description,omitempty"`
}

type targetHealthDescriptionXML struct {
	Target       targetDescriptionXML `xml:"Target"`
	TargetHealth *targetHealthXML     `xml:"TargetHealth,omitempty"`
}

type targetHealthDescriptionsXML struct {
	Member []targetHealthDescriptionXML `xml:"member,omitempty"`
}

type describeTargetHealthResult struct {
	TargetHealthDescriptions targetHealthDescriptionsXML `xml:"TargetHealthDescriptions"`
}

type registerTargetsResponse struct {
	XMLName  xml.Name         `xml:"RegisterTargetsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"RegisterTargetsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type deregisterTargetsResponse struct {
	XMLName  xml.Name         `xml:"DeregisterTargetsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   emptyResult      `xml:"DeregisterTargetsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type describeTargetHealthResponse struct {
	XMLName  xml.Name                   `xml:"DescribeTargetHealthResponse"`
	Xmlns    string                     `xml:"xmlns,attr"`
	Result   describeTargetHealthResult `xml:"DescribeTargetHealthResult"`
	Metadata responseMetadata           `xml:"ResponseMetadata"`
}

// toLoadBalancerXML converts a driver LBInfo to its XML representation.
func toLoadBalancerXML(lb *lbdriver.LBInfo) loadBalancerXML {
	out := loadBalancerXML{
		LoadBalancerArn:       lb.ARN,
		LoadBalancerName:      lb.Name,
		DNSName:               lb.DNSName,
		CanonicalHostedZoneId: lb.CanonicalHostedZoneID,
		Scheme:                lb.Scheme,
		Type:                  lb.Type,
		VpcID:                 lb.VPCID,
		IpAddressType:         lb.IPAddressType,
		State:                 &loadBalancerStateXML{Code: lb.State},
	}

	if !lb.CreatedTime.IsZero() {
		out.CreatedTime = lb.CreatedTime.UTC().Format(time.RFC3339)
	}

	if len(lb.Subnets) > 0 {
		az := &availabilityZonesXML{}
		for _, s := range lb.Subnets {
			az.Member = append(az.Member, availabilityZoneXML{
				ZoneName: zoneNameForSubnet(),
				SubnetID: s,
			})
		}

		out.AvailabilityZones = az
	}

	if len(lb.SecurityGroups) > 0 {
		out.SecurityGroups = &securityGroupsXML{Member: append([]string(nil), lb.SecurityGroups...)}
	}

	return out
}

// zoneNameForSubnet returns the availability-zone name reported for a subnet.
// The emulator does not model subnet placement, so it reports the region's
// first zone — enough to populate the non-empty ZoneName real ELBv2 returns.
func zoneNameForSubnet() string {
	return defaultAvailabilityZone
}

// toTargetGroupXML converts a driver TargetGroupInfo to its XML representation.
func toTargetGroupXML(tg *lbdriver.TargetGroupInfo) targetGroupXML {
	targetType := tg.TargetType
	if targetType == "" {
		targetType = "instance"
	}

	out := targetGroupXML{
		TargetGroupArn:             tg.ARN,
		TargetGroupName:            tg.Name,
		Protocol:                   tg.Protocol,
		Port:                       tg.Port,
		VpcID:                      tg.VPCID,
		TargetType:                 targetType,
		HealthCheckProtocol:        tg.HealthCheck.Protocol,
		HealthCheckPort:            tg.HealthCheck.Port,
		HealthCheckPath:            tg.HealthPath,
		HealthCheckEnabled:         true,
		HealthCheckIntervalSeconds: tg.HealthCheck.IntervalSeconds,
		HealthCheckTimeoutSeconds:  tg.HealthCheck.TimeoutSeconds,
		HealthyThresholdCount:      tg.HealthCheck.HealthyThreshold,
		UnhealthyThresholdCount:    tg.HealthCheck.UnhealthyThreshold,
	}

	if tg.HealthCheck.Matcher != "" {
		out.Matcher = &matcherXML{HttpCode: tg.HealthCheck.Matcher}
	}

	return out
}

// toListenerXML converts a driver ListenerInfo to its XML representation,
// echoing every stored default action (forward, redirect, fixed-response) so a
// listener round-trips exactly what it was created with.
func toListenerXML(li *lbdriver.ListenerInfo) listenerXML {
	return listenerXML{
		ListenerArn:     li.ARN,
		LoadBalancerArn: li.LBARN,
		Protocol:        li.Protocol,
		Port:            li.Port,
		SslPolicy:       li.SslPolicy,
		Certificates:    toCertificatesXML(li.Certificates),
		DefaultActions:  toActionsXML(li.DefaultActions),
	}
}

// toCertificatesXML renders a listener's certificate list, or nil when empty.
func toCertificatesXML(certs []lbdriver.Certificate) *certificatesXML {
	if len(certs) == 0 {
		return nil
	}

	out := &certificatesXML{Member: make([]certificateXML, 0, len(certs))}
	for _, c := range certs {
		out.Member = append(out.Member, certificateXML{
			CertificateArn: c.CertificateArn,
			IsDefault:      c.IsDefault,
		})
	}

	return out
}

// toActionsXML renders a driver action slice as ELBv2 action members, or nil
// when there are none. Shared by listener default actions and rule actions.
func toActionsXML(actions []lbdriver.RuleAction) *actionsXML {
	if len(actions) == 0 {
		return nil
	}

	out := &actionsXML{Member: make([]actionXML, 0, len(actions))}
	for i := range actions {
		out.Member = append(out.Member, toActionXML(actions[i]))
	}

	return out
}

// toActionXML renders a single driver action, preserving redirect and
// fixed-response configuration.
func toActionXML(a lbdriver.RuleAction) actionXML {
	x := actionXML{
		Type:           a.Type,
		TargetGroupArn: a.TargetGroupARN,
		Order:          a.Order,
	}

	if rc := a.RedirectConfig; rc != nil {
		x.RedirectConfig = &redirectConfigXML{
			Protocol:   rc.Protocol,
			Port:       rc.Port,
			Host:       rc.Host,
			Path:       rc.Path,
			Query:      rc.Query,
			StatusCode: rc.StatusCode,
		}
	}

	if fr := a.FixedResponseConfig; fr != nil {
		x.FixedResponseConfig = &fixedResponseConfigXML{
			StatusCode:  fr.StatusCode,
			ContentType: fr.ContentType,
			MessageBody: fr.MessageBody,
		}
	}

	return x
}
