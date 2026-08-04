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

// instanceTypeSpec is the vCPU/memory profile reported for an instance type.
type instanceTypeSpec struct {
	vcpus     int
	memoryMiB int
}

// knownInstanceTypes maps the common instance types to their specs. An
// unrecognized type still gets a response (small default) so validation calls
// don't fail on a type the emulator hasn't enumerated.
var knownInstanceTypes = map[string]instanceTypeSpec{ //nolint:gochecknoglobals // static lookup table
	"t2.micro":  {1, 1024},
	"t2.small":  {1, 2048},
	"t3.micro":  {2, 1024},
	"t3.small":  {2, 2048},
	"t3.medium": {2, 4096},
	"m5.large":  {2, 8192},
	"m5.xlarge": {4, 16384},
	"c5.large":  {2, 4096},
	"r5.large":  {2, 16384},
}

type vCPUInfoXML struct {
	DefaultVCpus int `xml:"defaultVCpus"`
}

type memoryInfoXML struct {
	SizeInMiB int `xml:"sizeInMiB"`
}

type instanceTypeInfoXML struct {
	InstanceType string        `xml:"instanceType"`
	VCPUInfo     vCPUInfoXML   `xml:"vCpuInfo"`
	MemoryInfo   memoryInfoXML `xml:"memoryInfo"`
}

type describeInstanceTypesResponseXML struct {
	XMLName       xml.Name              `xml:"DescribeInstanceTypesResponse"`
	Xmlns         string                `xml:"xmlns,attr"`
	RequestID     string                `xml:"requestId"`
	InstanceTypes []instanceTypeInfoXML `xml:"instanceTypeSet>item"`
}

// describeInstanceTypes answers ec2:DescribeInstanceTypes. Explicit
// InstanceType.N values are echoed with their (or a default) spec; with none
// supplied, the known set is reported.
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
			spec = instanceTypeSpec{vcpus: 2, memoryMiB: 4096}
		}

		out = append(out, instanceTypeInfoXML{
			InstanceType: name,
			VCPUInfo:     vCPUInfoXML{DefaultVCpus: spec.vcpus},
			MemoryInfo:   memoryInfoXML{SizeInMiB: spec.memoryMiB},
		})
	}

	awsquery.WriteXMLResponse(w, describeInstanceTypesResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, InstanceTypes: out,
	})
}
