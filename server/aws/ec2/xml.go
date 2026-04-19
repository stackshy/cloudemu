package ec2

import "encoding/xml"

// EC2 instance-state codes per the AWS API reference.
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_InstanceState.html
const (
	stateCodePending      = 0
	stateCodeRunning      = 16
	stateCodeShuttingDown = 32
	stateCodeTerminated   = 48
	stateCodeStopping     = 64
	stateCodeStopped      = 80

	// Canonical "owner" returned in responses. SDK clients don't validate it;
	// any 12-digit account id works.
	ownerID = "123456789012"
)

// stateCode maps the driver's string state to AWS's numeric code.
func stateCode(name string) int {
	switch name {
	case "pending":
		return stateCodePending
	case "running":
		return stateCodeRunning
	case "shutting-down":
		return stateCodeShuttingDown
	case "terminated":
		return stateCodeTerminated
	case "stopping":
		return stateCodeStopping
	case "stopped":
		return stateCodeStopped
	default:
		return stateCodePending
	}
}

// instanceState is the nested <instanceState> element.
type instanceState struct {
	Code int    `xml:"code"`
	Name string `xml:"name"`
}

// tagItem is one <tagSet><item>…</item></tagSet> entry.
type tagItem struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

// groupItem is one <groupSet><item>…</item></groupSet> entry (security group).
type groupItem struct {
	GroupID string `xml:"groupId"`
}

// instanceXML is the per-instance payload shared by RunInstances and
// DescribeInstances responses. We populate only the fields the SDK reliably
// consumes and real apps actually read; unused AWS fields are omitted.
type instanceXML struct {
	InstanceID   string        `xml:"instanceId"`
	ImageID      string        `xml:"imageId"`
	State        instanceState `xml:"instanceState"`
	InstanceType string        `xml:"instanceType"`
	LaunchTime   string        `xml:"launchTime,omitempty"`
	SubnetID     string        `xml:"subnetId,omitempty"`
	VPCID        string        `xml:"vpcId,omitempty"`
	PrivateIP    string        `xml:"privateIpAddress,omitempty"`
	PublicIP     string        `xml:"ipAddress,omitempty"`
	KeyName      string        `xml:"keyName,omitempty"`
	Groups       []groupItem   `xml:"groupSet>item,omitempty"`
	Tags         []tagItem     `xml:"tagSet>item,omitempty"`
}

// runInstancesResponse is the XML body for RunInstances.
type runInstancesResponse struct {
	XMLName       xml.Name      `xml:"RunInstancesResponse"`
	Xmlns         string        `xml:"xmlns,attr"`
	RequestID     string        `xml:"requestId"`
	ReservationID string        `xml:"reservationId"`
	OwnerID       string        `xml:"ownerId"`
	Instances     []instanceXML `xml:"instancesSet>item"`
}

// reservationXML is one item in a DescribeInstances <reservationSet>.
type reservationXML struct {
	ReservationID string        `xml:"reservationId"`
	OwnerID       string        `xml:"ownerId"`
	Instances     []instanceXML `xml:"instancesSet>item"`
}

// describeInstancesResponse is the XML body for DescribeInstances.
type describeInstancesResponse struct {
	XMLName      xml.Name         `xml:"DescribeInstancesResponse"`
	Xmlns        string           `xml:"xmlns,attr"`
	RequestID    string           `xml:"requestId"`
	Reservations []reservationXML `xml:"reservationSet>item"`
}

// stateChangeXML is one item in Start/Stop/Terminate responses.
type stateChangeXML struct {
	InstanceID    string        `xml:"instanceId"`
	CurrentState  instanceState `xml:"currentState"`
	PreviousState instanceState `xml:"previousState"`
}

// startInstancesResponse — same shape used by StopInstances and
// TerminateInstances (with different XMLName).
type startInstancesResponse struct {
	XMLName   xml.Name         `xml:"StartInstancesResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	Changes   []stateChangeXML `xml:"instancesSet>item"`
}

type stopInstancesResponse struct {
	XMLName   xml.Name         `xml:"StopInstancesResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	Changes   []stateChangeXML `xml:"instancesSet>item"`
}

type terminateInstancesResponse struct {
	XMLName   xml.Name         `xml:"TerminateInstancesResponse"`
	Xmlns     string           `xml:"xmlns,attr"`
	RequestID string           `xml:"requestId"`
	Changes   []stateChangeXML `xml:"instancesSet>item"`
}

// rebootInstancesResponse is a simple boolean-return shape.
type rebootInstancesResponse struct {
	XMLName   xml.Name `xml:"RebootInstancesResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// modifyInstanceAttributeResponse — same boolean-return shape as Reboot.
type modifyInstanceAttributeResponse struct {
	XMLName   xml.Name `xml:"ModifyInstanceAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}
