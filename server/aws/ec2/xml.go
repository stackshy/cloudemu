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

	// stateRunning is the driver's string name for a running instance.
	stateRunning = "running"
)

// stateCode maps the driver's string state to AWS's numeric code.
func stateCode(name string) int {
	switch name {
	case "pending":
		return stateCodePending
	case stateRunning:
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
// VPC instances report both the id and the resolved name.
type groupItem struct {
	GroupID   string `xml:"groupId"`
	GroupName string `xml:"groupName,omitempty"`
}

// placementXML is the nested <placement> element carrying the instance's
// availability zone and tenancy.
type placementXML struct {
	AvailabilityZone string `xml:"availabilityZone,omitempty"`
	Tenancy          string `xml:"tenancy,omitempty"`
}

// monitoringXML is the nested <monitoring> element carrying the detailed-
// monitoring state (disabled/disabling/enabled/pending).
type monitoringXML struct {
	State string `xml:"state"`
}

// metadataOptionsXML is the nested <metadataOptions> element carrying an
// instance's IMDS configuration.
type metadataOptionsXML struct {
	State                   string `xml:"state,omitempty"`
	HTTPTokens              string `xml:"httpTokens,omitempty"`
	HTTPPutResponseHopLimit int    `xml:"httpPutResponseHopLimit,omitempty"`
	HTTPEndpoint            string `xml:"httpEndpoint,omitempty"`
	HTTPProtocolIPv6        string `xml:"httpProtocolIpv6,omitempty"`
	InstanceMetadataTags    string `xml:"instanceMetadataTags,omitempty"`
}

// iamInstanceProfileXML is the nested <iamInstanceProfile> element carrying the
// IAM instance profile attached to an instance. The child element names (arn,
// id) match the ec2 IamInstanceProfile response shape the aws-sdk-go-v2
// deserializer binds to Instance.IamInstanceProfile.
type iamInstanceProfileXML struct {
	ARN string `xml:"arn,omitempty"`
	ID  string `xml:"id,omitempty"`
}

// instanceENIAttachmentXML is an instance ENI's <attachment>: the device index
// (0 for eth0), the attachment id, and the attachment status.
type instanceENIAttachmentXML struct {
	AttachmentID string `xml:"attachmentId,omitempty"`
	DeviceIndex  int    `xml:"deviceIndex"`
	Status       string `xml:"status"`
}

// instanceENIXML is one <networkInterfaceSet><item> describing one of the
// instance's elastic network interfaces, as embedded in a DescribeInstances
// item. It is distinct from the standalone networkInterfaceXML
// (DescribeNetworkInterfaces) because the embedded shape carries
// privateIpAddress, groupSet and the per-attachment deviceIndex.
type instanceENIXML struct {
	NetworkInterfaceID string                   `xml:"networkInterfaceId,omitempty"`
	SubnetID           string                   `xml:"subnetId,omitempty"`
	VPCID              string                   `xml:"vpcId,omitempty"`
	MacAddress         string                   `xml:"macAddress,omitempty"`
	PrivateIP          string                   `xml:"privateIpAddress,omitempty"`
	Status             string                   `xml:"status,omitempty"`
	SourceDestCheck    bool                     `xml:"sourceDestCheck"`
	Groups             []groupItem              `xml:"groupSet>item,omitempty"`
	Attachment         instanceENIAttachmentXML `xml:"attachment"`
}

// instanceBlockDeviceXML is one <blockDeviceMapping><item> in a DescribeInstances
// item: the device name plus the EBS volume attached at that device.
type instanceBlockDeviceXML struct {
	DeviceName string         `xml:"deviceName"`
	EBS        instanceEBSXML `xml:"ebs"`
}

// instanceEBSXML is the <ebs> child of an instance block-device mapping,
// carrying the attached volume's id and attachment state.
type instanceEBSXML struct {
	VolumeID            string `xml:"volumeId"`
	Status              string `xml:"status"`
	AttachTime          string `xml:"attachTime,omitempty"`
	DeleteOnTermination bool   `xml:"deleteOnTermination"`
}

// operatorXML is the nested <operator> element carrying managed-resource
// ownership. The child element names match the ec2 OperatorResponse fields the
// aws-sdk-go-v2 deserializer reads case-insensitively (managed, principal,
// hiddenByDefault); verified against service/ec2 deserializers.go, which does
// strings.EqualFold("hiddenByDefault", ...) into OperatorResponse.HiddenByDefault.
type operatorXML struct {
	Managed         bool   `xml:"managed"`
	Principal       string `xml:"principal,omitempty"`
	HiddenByDefault bool   `xml:"hiddenByDefault,omitempty"`
}

// instanceXML is the per-instance payload shared by RunInstances and
// DescribeInstances responses. We populate only the fields the SDK reliably
// consumes and real apps actually read; unused AWS fields are omitted.
type instanceXML struct {
	InstanceID          string                   `xml:"instanceId"`
	ImageID             string                   `xml:"imageId"`
	State               instanceState            `xml:"instanceState"`
	InstanceType        string                   `xml:"instanceType"`
	LaunchTime          string                   `xml:"launchTime,omitempty"`
	SubnetID            string                   `xml:"subnetId,omitempty"`
	VPCID               string                   `xml:"vpcId,omitempty"`
	PrivateIP           string                   `xml:"privateIpAddress,omitempty"`
	PublicIP            string                   `xml:"ipAddress,omitempty"`
	PrivateDNSName      string                   `xml:"privateDnsName,omitempty"`
	PublicDNSName       string                   `xml:"dnsName,omitempty"`
	KeyName             string                   `xml:"keyName,omitempty"`
	AmiLaunchIndex      int                      `xml:"amiLaunchIndex"`
	Architecture        string                   `xml:"architecture,omitempty"`
	RootDeviceType      string                   `xml:"rootDeviceType,omitempty"`
	RootDeviceName      string                   `xml:"rootDeviceName,omitempty"`
	VirtualizationType  string                   `xml:"virtualizationType,omitempty"`
	Hypervisor          string                   `xml:"hypervisor,omitempty"`
	Placement           *placementXML            `xml:"placement,omitempty"`
	Monitoring          *monitoringXML           `xml:"monitoring,omitempty"`
	MetadataOptions     *metadataOptionsXML      `xml:"metadataOptions,omitempty"`
	IamInstanceProfile  *iamInstanceProfileXML   `xml:"iamInstanceProfile,omitempty"`
	Groups              []groupItem              `xml:"groupSet>item,omitempty"`
	NetworkInterfaces   []instanceENIXML         `xml:"networkInterfaceSet>item,omitempty"`
	BlockDeviceMappings []instanceBlockDeviceXML `xml:"blockDeviceMapping>item,omitempty"`
	Tags                []tagItem                `xml:"tagSet>item,omitempty"`
	Operator            *operatorXML             `xml:"operator,omitempty"`
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
	NextToken    string           `xml:"nextToken,omitempty"`
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

// attributeBooleanValueXML / attributeValueXML wrap a single attribute value in
// DescribeInstanceAttribute responses. The SDK reads the nested <value>.
type attributeBooleanValueXML struct {
	Value bool `xml:"value"`
}

type attributeValueXML struct {
	Value string `xml:"value"`
}

// describeInstanceAttributeResponse carries exactly one requested attribute
// (the others stay nil / omitted), matching real EC2's per-attribute response.
type describeInstanceAttributeResponse struct {
	XMLName               xml.Name                  `xml:"DescribeInstanceAttributeResponse"`
	Xmlns                 string                    `xml:"xmlns,attr"`
	RequestID             string                    `xml:"requestId"`
	InstanceID            string                    `xml:"instanceId"`
	DisableAPITermination *attributeBooleanValueXML `xml:"disableApiTermination,omitempty"`
	SourceDestCheck       *attributeBooleanValueXML `xml:"sourceDestCheck,omitempty"`
	EBSOptimized          *attributeBooleanValueXML `xml:"ebsOptimized,omitempty"`
	DisableAPIStop        *attributeBooleanValueXML `xml:"disableApiStop,omitempty"`
	InstanceType          *attributeValueXML        `xml:"instanceType,omitempty"`
	UserData              *attributeValueXML        `xml:"userData,omitempty"`
	ShutdownBehavior      *attributeValueXML        `xml:"instanceInitiatedShutdownBehavior,omitempty"`
	Groups                []groupItem               `xml:"groupSet>item,omitempty"`
}

// getConsoleOutputResponse carries the base64-encoded console output for an
// instance. Output mirrors real EC2's base64 <output> field.
type getConsoleOutputResponse struct {
	XMLName    xml.Name `xml:"GetConsoleOutputResponse"`
	Xmlns      string   `xml:"xmlns,attr"`
	RequestID  string   `xml:"requestId"`
	InstanceID string   `xml:"instanceId"`
	Timestamp  string   `xml:"timestamp"`
	Output     string   `xml:"output"`
}
