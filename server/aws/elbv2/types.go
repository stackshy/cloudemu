package elbv2

import (
	"encoding/xml"

	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

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
	SubnetID string `xml:"SubnetId,omitempty"`
}

type availabilityZonesXML struct {
	Member []availabilityZoneXML `xml:"member,omitempty"`
}

type loadBalancerXML struct {
	LoadBalancerArn   string                `xml:"LoadBalancerArn"`
	LoadBalancerName  string                `xml:"LoadBalancerName"`
	DNSName           string                `xml:"DNSName,omitempty"`
	Scheme            string                `xml:"Scheme,omitempty"`
	Type              string                `xml:"Type,omitempty"`
	VpcID             string                `xml:"VpcId,omitempty"`
	State             *loadBalancerStateXML `xml:"State,omitempty"`
	AvailabilityZones *availabilityZonesXML `xml:"AvailabilityZones,omitempty"`
}

type loadBalancersXML struct {
	Member []loadBalancerXML `xml:"member,omitempty"`
}

type loadBalancersResult struct {
	LoadBalancers loadBalancersXML `xml:"LoadBalancers"`
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
	TargetGroupArn  string `xml:"TargetGroupArn"`
	TargetGroupName string `xml:"TargetGroupName"`
	Protocol        string `xml:"Protocol,omitempty"`
	Port            int    `xml:"Port,omitempty"`
	VpcID           string `xml:"VpcId,omitempty"`
	TargetType      string `xml:"TargetType,omitempty"`
	HealthCheckPath string `xml:"HealthCheckPath,omitempty"`
}

type targetGroupsXML struct {
	Member []targetGroupXML `xml:"member,omitempty"`
}

type targetGroupsResult struct {
	TargetGroups targetGroupsXML `xml:"TargetGroups"`
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

type actionXML struct {
	Type           string `xml:"Type"`
	TargetGroupArn string `xml:"TargetGroupArn,omitempty"`
	Order          int    `xml:"Order,omitempty"`
}

type actionsXML struct {
	Member []actionXML `xml:"member,omitempty"`
}

type listenerXML struct {
	ListenerArn     string      `xml:"ListenerArn"`
	LoadBalancerArn string      `xml:"LoadBalancerArn,omitempty"`
	Protocol        string      `xml:"Protocol,omitempty"`
	Port            int         `xml:"Port,omitempty"`
	DefaultActions  *actionsXML `xml:"DefaultActions,omitempty"`
}

type listenersXML struct {
	Member []listenerXML `xml:"member,omitempty"`
}

type listenersResult struct {
	Listeners listenersXML `xml:"Listeners"`
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

type ruleConditionXML struct {
	Field  string         `xml:"Field,omitempty"`
	Values *stringListXML `xml:"Values,omitempty"`
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
	Rules rulesXML `xml:"Rules"`
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
	State  string `xml:"State,omitempty"`
	Reason string `xml:"Reason,omitempty"`
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
		LoadBalancerArn:  lb.ARN,
		LoadBalancerName: lb.Name,
		DNSName:          lb.DNSName,
		Scheme:           lb.Scheme,
		Type:             lb.Type,
		State:            &loadBalancerStateXML{Code: lb.State},
	}

	if len(lb.Subnets) > 0 {
		az := &availabilityZonesXML{}
		for _, s := range lb.Subnets {
			az.Member = append(az.Member, availabilityZoneXML{SubnetID: s})
		}

		out.AvailabilityZones = az
	}

	return out
}

// toTargetGroupXML converts a driver TargetGroupInfo to its XML representation.
func toTargetGroupXML(tg *lbdriver.TargetGroupInfo) targetGroupXML {
	return targetGroupXML{
		TargetGroupArn:  tg.ARN,
		TargetGroupName: tg.Name,
		Protocol:        tg.Protocol,
		Port:            tg.Port,
		VpcID:           tg.VPCID,
		TargetType:      "instance",
		HealthCheckPath: tg.HealthPath,
	}
}

// toListenerXML converts a driver ListenerInfo to its XML representation. The
// forward-to-target-group default action is reconstructed from the stored
// TargetGroupARN.
func toListenerXML(li *lbdriver.ListenerInfo) listenerXML {
	out := listenerXML{
		ListenerArn:     li.ARN,
		LoadBalancerArn: li.LBARN,
		Protocol:        li.Protocol,
		Port:            li.Port,
	}

	if li.TargetGroupARN != "" {
		out.DefaultActions = &actionsXML{Member: []actionXML{{
			Type:           "forward",
			TargetGroupArn: li.TargetGroupARN,
			Order:          1,
		}}}
	}

	return out
}
