package ec2

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// azCount is how many zones a region reports.
//
// Three is the meaningful number, not an arbitrary one: it is the minimum real
// AWS regions offer, and it is what makes multi-AZ provisioning paths
// exercisable. A subnet group spanning two AZs — which RDS requires — cannot be
// built against a region that reports fewer, so anything less would make
// datastore provisioning untestable rather than merely approximate.
const azCount = 3

// regionFromRequest reads the region out of the SigV4 credential scope
// ("Credential=AK/20260727/us-east-1/ec2/aws4_request"). The query API carries
// no region parameter, and the signature is the only place a caller states
// which region it believes it is talking to — so deriving it here keeps the
// answer consistent with what the caller asked for rather than pinning every
// caller to one hard-coded region.
func regionFromRequest(r *http.Request) string {
	const fallback = "us-east-1"

	auth := r.Header.Get("Authorization")

	i := strings.Index(auth, "Credential=")
	if i < 0 {
		return fallback
	}

	parts := strings.Split(auth[i+len("Credential="):], "/")
	if len(parts) < 3 || parts[2] == "" {
		return fallback
	}

	return parts[2]
}

type availabilityZoneXML struct {
	ZoneName   string `xml:"zoneName"`
	ZoneID     string `xml:"zoneId"`
	State      string `xml:"zoneState"`
	RegionName string `xml:"regionName"`
}

type describeAZResponseXML struct {
	XMLName          xml.Name              `xml:"DescribeAvailabilityZonesResponse"`
	Xmlns            string                `xml:"xmlns,attr"`
	RequestID        string                `xml:"requestId"`
	AvailabilityZone []availabilityZoneXML `xml:"availabilityZoneInfo>item"`
}

// describeAvailabilityZones answers ec2:DescribeAvailabilityZones.
//
// Provisioning a VPC is the first step of almost every datastore plan, and
// picking subnets requires knowing the region's zones — so without this action
// the very first step of a datastore job fails with "unknown action", before
// any of the VPC/subnet/RDS behaviour the emulator does implement is reached.
//
// Zones are derived from the requested region (us-east-1 -> us-east-1a/b/c)
// rather than served from a fixed table, so any region a caller uses behaves
// consistently instead of only the ones someone remembered to enumerate.
func (h *Handler) describeAvailabilityZones(w http.ResponseWriter, r *http.Request) {
	region := regionFromRequest(r)

	zones := make([]availabilityZoneXML, 0, azCount)

	for i := range azCount {
		suffix := string(rune('a' + i))
		zones = append(zones, availabilityZoneXML{
			ZoneName:   region + suffix,
			ZoneID:     region + "-az" + string(rune('1'+i)),
			State:      "available",
			RegionName: region,
		})
	}

	awsquery.WriteXMLResponse(w, describeAZResponseXML{
		Xmlns:            awsquery.Namespace,
		RequestID:        awsquery.RequestID,
		AvailabilityZone: zones,
	})
}
