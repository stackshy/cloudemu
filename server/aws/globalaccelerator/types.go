package globalaccelerator

import (
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// tagJSON is the wire shape of a Global Accelerator tag: an object with
// PascalCase Key/Value members.
type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// tagsToWire renders a tag map as a Key-sorted list, so repeated reads are
// byte-stable regardless of map iteration order.
func tagsToWire(tags map[string]string) []tagJSON {
	if tags == nil {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]tagJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagJSON{Key: k, Value: tags[k]})
	}

	return out
}

// tagsFromWire converts a wire tag list into a map.
func tagsFromWire(tags []tagJSON) map[string]string {
	if tags == nil {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

// toWireList maps a slice of driver pointers to their wire shapes, centralizing
// the list-response mechanics the accelerator, listener and endpoint-group list
// operations share.
func toWireList[T, W any](items []*T, conv func(*T) W) []W {
	out := make([]W, 0, len(items))
	for _, it := range items {
		out = append(out, conv(it))
	}

	return out
}

// millisPerSecond converts milliseconds to the fractional epoch seconds Global
// Accelerator timestamps use on the wire.
const millisPerSecond = 1000.0

// epochSeconds renders a timestamp as JSON epoch seconds, or nil for the zero
// time so an unset timestamp reads back as null.
func epochSeconds(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return float64(t.UTC().UnixMilli()) / millisPerSecond
}

// ipSetJSON is the wire shape of an accelerator IP set.
type ipSetJSON struct {
	IPFamily        string   `json:"IpFamily,omitempty"`
	IPAddresses     []string `json:"IpAddresses,omitempty"`
	IPAddressFamily string   `json:"IpAddressFamily,omitempty"`
}

func ipSetsToWire(in []driver.IPSet) []ipSetJSON {
	if in == nil {
		return nil
	}

	out := make([]ipSetJSON, len(in))
	for i := range in {
		out[i] = ipSetJSON{
			IPFamily:        in[i].IPFamily,
			IPAddresses:     in[i].IPAddresses,
			IPAddressFamily: in[i].IPAddressFamily,
		}
	}

	return out
}

// acceleratorJSON is the wire shape of an Accelerator.
type acceleratorJSON struct {
	AcceleratorArn   string      `json:"AcceleratorArn"`
	Name             string      `json:"Name"`
	IPAddressType    string      `json:"IpAddressType"`
	Enabled          bool        `json:"Enabled"`
	IPSets           []ipSetJSON `json:"IpSets,omitempty"`
	DNSName          string      `json:"DnsName"`
	DualStackDNSName string      `json:"DualStackDnsName,omitempty"`
	Status           string      `json:"Status"`
	CreatedTime      any         `json:"CreatedTime,omitempty"`
	LastModifiedTime any         `json:"LastModifiedTime,omitempty"`
}

func acceleratorToWire(a *driver.Accelerator) acceleratorJSON {
	return acceleratorJSON{
		AcceleratorArn:   a.AcceleratorArn,
		Name:             a.Name,
		IPAddressType:    a.IPAddressType,
		Enabled:          a.Enabled,
		IPSets:           ipSetsToWire(a.IPSets),
		DNSName:          a.DNSName,
		DualStackDNSName: a.DualStackDNSName,
		Status:           a.Status,
		CreatedTime:      epochSeconds(a.CreatedTime),
		LastModifiedTime: epochSeconds(a.LastModifiedTime),
	}
}

// acceleratorAttributesJSON is the wire shape of AcceleratorAttributes.
type acceleratorAttributesJSON struct {
	FlowLogsEnabled  bool   `json:"FlowLogsEnabled"`
	FlowLogsS3Bucket string `json:"FlowLogsS3Bucket,omitempty"`
	FlowLogsS3Prefix string `json:"FlowLogsS3Prefix,omitempty"`
}

func attributesToWire(a *driver.AcceleratorAttributes) acceleratorAttributesJSON {
	return acceleratorAttributesJSON{
		FlowLogsEnabled:  a.FlowLogsEnabled,
		FlowLogsS3Bucket: a.FlowLogsS3Bucket,
		FlowLogsS3Prefix: a.FlowLogsS3Prefix,
	}
}

// portRangeJSON is the wire shape of a listener port range.
type portRangeJSON struct {
	FromPort int32 `json:"FromPort"`
	ToPort   int32 `json:"ToPort"`
}

func portRangesToWire(in []driver.PortRange) []portRangeJSON {
	if in == nil {
		return nil
	}

	out := make([]portRangeJSON, len(in))
	for i := range in {
		out[i] = portRangeJSON{FromPort: in[i].FromPort, ToPort: in[i].ToPort}
	}

	return out
}

func portRangesFromWire(in []portRangeJSON) []driver.PortRange {
	if in == nil {
		return nil
	}

	out := make([]driver.PortRange, len(in))
	for i := range in {
		out[i] = driver.PortRange{FromPort: in[i].FromPort, ToPort: in[i].ToPort}
	}

	return out
}

// listenerJSON is the wire shape of a Listener.
type listenerJSON struct {
	ListenerArn    string          `json:"ListenerArn"`
	PortRanges     []portRangeJSON `json:"PortRanges,omitempty"`
	Protocol       string          `json:"Protocol"`
	ClientAffinity string          `json:"ClientAffinity"`
}

func listenerToWire(l *driver.Listener) listenerJSON {
	return listenerJSON{
		ListenerArn:    l.ListenerArn,
		PortRanges:     portRangesToWire(l.PortRanges),
		Protocol:       l.Protocol,
		ClientAffinity: l.ClientAffinity,
	}
}

// endpointConfigurationJSON is the wire shape of an endpoint configuration
// (create/update input).
type endpointConfigurationJSON struct {
	EndpointID                  string `json:"EndpointId"`
	Weight                      *int32 `json:"Weight"`
	ClientIPPreservationEnabled *bool  `json:"ClientIPPreservationEnabled"`
	AttachmentArn               string `json:"AttachmentArn,omitempty"`
}

func endpointConfigsFromWire(in []endpointConfigurationJSON) []driver.EndpointConfiguration {
	if in == nil {
		return nil
	}

	out := make([]driver.EndpointConfiguration, len(in))
	for i := range in {
		out[i] = driver.EndpointConfiguration{
			EndpointID:                  in[i].EndpointID,
			Weight:                      in[i].Weight,
			ClientIPPreservationEnabled: in[i].ClientIPPreservationEnabled,
			AttachmentArn:               in[i].AttachmentArn,
		}
	}

	return out
}

// endpointDescriptionJSON is the wire shape of an endpoint description (read
// output).
type endpointDescriptionJSON struct {
	EndpointID                  string `json:"EndpointId"`
	Weight                      *int32 `json:"Weight"`
	HealthState                 string `json:"HealthState,omitempty"`
	HealthReason                string `json:"HealthReason,omitempty"`
	ClientIPPreservationEnabled *bool  `json:"ClientIPPreservationEnabled"`
	AttachmentArn               string `json:"AttachmentArn,omitempty"`
}

func endpointDescriptionsToWire(in []driver.EndpointDescription) []endpointDescriptionJSON {
	if in == nil {
		return nil
	}

	out := make([]endpointDescriptionJSON, len(in))
	for i := range in {
		out[i] = endpointDescriptionJSON{
			EndpointID:                  in[i].EndpointID,
			Weight:                      in[i].Weight,
			HealthState:                 in[i].HealthState,
			HealthReason:                in[i].HealthReason,
			ClientIPPreservationEnabled: in[i].ClientIPPreservationEnabled,
			AttachmentArn:               in[i].AttachmentArn,
		}
	}

	return out
}

// portOverrideJSON is the wire shape of a port override.
type portOverrideJSON struct {
	ListenerPort int32 `json:"ListenerPort"`
	EndpointPort int32 `json:"EndpointPort"`
}

func portOverridesToWire(in []driver.PortOverride) []portOverrideJSON {
	if in == nil {
		return nil
	}

	out := make([]portOverrideJSON, len(in))
	for i := range in {
		out[i] = portOverrideJSON{ListenerPort: in[i].ListenerPort, EndpointPort: in[i].EndpointPort}
	}

	return out
}

func portOverridesFromWire(in []portOverrideJSON) []driver.PortOverride {
	if in == nil {
		return nil
	}

	out := make([]driver.PortOverride, len(in))
	for i := range in {
		out[i] = driver.PortOverride{ListenerPort: in[i].ListenerPort, EndpointPort: in[i].EndpointPort}
	}

	return out
}

// endpointGroupJSON is the wire shape of an EndpointGroup.
type endpointGroupJSON struct {
	EndpointGroupArn           string                    `json:"EndpointGroupArn"`
	EndpointGroupRegion        string                    `json:"EndpointGroupRegion"`
	EndpointDescriptions       []endpointDescriptionJSON `json:"EndpointDescriptions,omitempty"`
	TrafficDialPercentage      *float64                  `json:"TrafficDialPercentage"`
	HealthCheckPort            *int32                    `json:"HealthCheckPort"`
	HealthCheckProtocol        string                    `json:"HealthCheckProtocol,omitempty"`
	HealthCheckPath            string                    `json:"HealthCheckPath,omitempty"`
	HealthCheckIntervalSeconds *int32                    `json:"HealthCheckIntervalSeconds"`
	ThresholdCount             *int32                    `json:"ThresholdCount"`
	PortOverrides              []portOverrideJSON        `json:"PortOverrides,omitempty"`
}

func endpointGroupToWire(g *driver.EndpointGroup) endpointGroupJSON {
	return endpointGroupJSON{
		EndpointGroupArn:           g.EndpointGroupArn,
		EndpointGroupRegion:        g.EndpointGroupRegion,
		EndpointDescriptions:       endpointDescriptionsToWire(g.EndpointDescriptions),
		TrafficDialPercentage:      g.TrafficDialPercentage,
		HealthCheckPort:            g.HealthCheckPort,
		HealthCheckProtocol:        g.HealthCheckProtocol,
		HealthCheckPath:            g.HealthCheckPath,
		HealthCheckIntervalSeconds: g.HealthCheckIntervalSeconds,
		ThresholdCount:             g.ThresholdCount,
		PortOverrides:              portOverridesToWire(g.PortOverrides),
	}
}
