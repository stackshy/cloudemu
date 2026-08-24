package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// commonRegions is the representative region set DescribeRegions reports. Tools
// call DescribeRegions to validate a region exists before provisioning; a fixed
// common set satisfies that without pretending to enumerate every AWS region.
var commonRegions = []string{ //nolint:gochecknoglobals // static lookup table
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"eu-west-1", "eu-west-2", "eu-central-1",
	"ap-south-1", "ap-southeast-1", "ap-southeast-2", "ap-northeast-1",
}

type regionXML struct {
	RegionName  string `xml:"regionName"`
	Endpoint    string `xml:"regionEndpoint"`
	OptInStatus string `xml:"optInStatus"`
}

type describeRegionsResponseXML struct {
	XMLName   xml.Name    `xml:"DescribeRegionsResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	RequestID string      `xml:"requestId"`
	Regions   []regionXML `xml:"regionInfo>item"`
}

func (h *Handler) routeMetadata(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "DescribeRegions":
		h.describeRegions(w, r)
	case "DescribeInstanceTypes":
		h.describeInstanceTypes(w, r)
	default:
		return false
	}

	return true
}

// describeRegions answers ec2:DescribeRegions. If explicit RegionName.N filters
// are supplied, only those are returned; otherwise the common set is reported.
func (*Handler) describeRegions(w http.ResponseWriter, r *http.Request) {
	requested := awsquery.ListStrings(r.Form, "RegionName")

	names := commonRegions
	if len(requested) > 0 {
		names = requested
	}

	out := make([]regionXML, 0, len(names))
	for _, name := range names {
		out = append(out, regionXML{
			RegionName:  name,
			Endpoint:    "ec2." + name + ".amazonaws.com",
			OptInStatus: "opt-in-not-required",
		})
	}

	awsquery.WriteXMLResponse(w, describeRegionsResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Regions: out,
	})
}

// instanceTypeSpec is the profile reported for an instance type: vCPU/memory
// plus the processor and network facts real DescribeInstanceTypes returns.
type instanceTypeSpec struct {
	vcpus       int
	memoryMiB   int
	clockGHz    float64
	networkPerf string
	maxENIs     int
	ipsPerENI   int
}

// knownInstanceTypes maps the common instance types to their specs. Unlike
// before, an unrecognized *explicitly requested* type is rejected with
// InvalidInstanceType (matching real EC2) rather than fabricated.
var knownInstanceTypes = map[string]instanceTypeSpec{ //nolint:gochecknoglobals // static lookup table
	"t2.micro":  {vcpus: 1, memoryMiB: 1024, clockGHz: 3.3, networkPerf: "Low to Moderate", maxENIs: 2, ipsPerENI: 2},
	"t2.small":  {vcpus: 1, memoryMiB: 2048, clockGHz: 3.3, networkPerf: "Low to Moderate", maxENIs: 3, ipsPerENI: 4},
	"t3.micro":  {vcpus: 2, memoryMiB: 1024, clockGHz: 3.1, networkPerf: "Up to 5 Gigabit", maxENIs: 2, ipsPerENI: 2},
	"t3.small":  {vcpus: 2, memoryMiB: 2048, clockGHz: 3.1, networkPerf: "Up to 5 Gigabit", maxENIs: 3, ipsPerENI: 4},
	"t3.medium": {vcpus: 2, memoryMiB: 4096, clockGHz: 3.1, networkPerf: "Up to 5 Gigabit", maxENIs: 3, ipsPerENI: 6},
	"m5.large":  {vcpus: 2, memoryMiB: 8192, clockGHz: 3.1, networkPerf: "Up to 10 Gigabit", maxENIs: 3, ipsPerENI: 10},
	"m5.xlarge": {vcpus: 4, memoryMiB: 16384, clockGHz: 3.1, networkPerf: "Up to 10 Gigabit", maxENIs: 4, ipsPerENI: 15},
	"c5.large":  {vcpus: 2, memoryMiB: 4096, clockGHz: 3.4, networkPerf: "Up to 10 Gigabit", maxENIs: 3, ipsPerENI: 10},
	"r5.large":  {vcpus: 2, memoryMiB: 16384, clockGHz: 3.1, networkPerf: "Up to 10 Gigabit", maxENIs: 3, ipsPerENI: 10},
}

type vCPUInfoXML struct {
	DefaultVCpus int `xml:"defaultVCpus"`
}

type memoryInfoXML struct {
	SizeInMiB int `xml:"sizeInMiB"`
}

type processorInfoXML struct {
	SupportedArchitectures   []string `xml:"supportedArchitectures>item"`
	SustainedClockSpeedInGhz float64  `xml:"sustainedClockSpeedInGhz"`
}

type networkInfoXML struct {
	NetworkPerformance        string `xml:"networkPerformance"`
	MaximumNetworkInterfaces  int    `xml:"maximumNetworkInterfaces"`
	Ipv4AddressesPerInterface int    `xml:"ipv4AddressesPerInterface"`
}

type instanceTypeInfoXML struct {
	InstanceType      string           `xml:"instanceType"`
	CurrentGeneration bool             `xml:"currentGeneration"`
	VCPUInfo          vCPUInfoXML      `xml:"vCpuInfo"`
	MemoryInfo        memoryInfoXML    `xml:"memoryInfo"`
	ProcessorInfo     processorInfoXML `xml:"processorInfo"`
	NetworkInfo       networkInfoXML   `xml:"networkInfo"`
}

type describeInstanceTypesResponseXML struct {
	XMLName       xml.Name              `xml:"DescribeInstanceTypesResponse"`
	Xmlns         string                `xml:"xmlns,attr"`
	RequestID     string                `xml:"requestId"`
	InstanceTypes []instanceTypeInfoXML `xml:"instanceTypeSet>item"`
}

// describeInstanceTypes answers ec2:DescribeInstanceTypes. Explicit
// InstanceType.N values are echoed with their spec; an unrecognized explicit
// type is rejected with InvalidInstanceType (real EC2). With none supplied, the
// whole known set is reported.
func (*Handler) describeInstanceTypes(w http.ResponseWriter, r *http.Request) {
	requested := awsquery.ListStrings(r.Form, "InstanceType")

	names := requested
	if len(names) == 0 {
		for name := range knownInstanceTypes {
			names = append(names, name)
		}
	}

	out := make([]instanceTypeInfoXML, 0, len(names))

	for _, name := range names {
		spec, ok := knownInstanceTypes[name]
		if !ok {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidInstanceType",
				"The instance type '"+name+"' is not supported")

			return
		}

		out = append(out, instanceTypeInfoXML{
			InstanceType:      name,
			CurrentGeneration: true,
			VCPUInfo:          vCPUInfoXML{DefaultVCpus: spec.vcpus},
			MemoryInfo:        memoryInfoXML{SizeInMiB: spec.memoryMiB},
			ProcessorInfo: processorInfoXML{
				SupportedArchitectures:   []string{archX86},
				SustainedClockSpeedInGhz: spec.clockGHz,
			},
			NetworkInfo: networkInfoXML{
				NetworkPerformance:        spec.networkPerf,
				MaximumNetworkInterfaces:  spec.maxENIs,
				Ipv4AddressesPerInterface: spec.ipsPerENI,
			},
		})
	}

	awsquery.WriteXMLResponse(w, describeInstanceTypesResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, InstanceTypes: out,
	})
}
