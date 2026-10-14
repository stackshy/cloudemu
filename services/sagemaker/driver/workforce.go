package driver

import "context"

// Workforce status values.
const (
	WorkforceInitializing = "Initializing"
	WorkforceUpdating     = "Updating"
	WorkforceActive       = "Active"
)

// Workforce and workteam limits.
const (
	MaxWorkteams         = 25
	MaxWorkteamMembers   = 10
	MaxWorkforceCIDRs    = 10
	DefaultWorkforceName = "default"
)

// MemberDefinition is one member group of a workteam: a Cognito user group or
// a set of OIDC groups, never both.
type MemberDefinition struct {
	CognitoClientID  string
	CognitoUserPool  string
	CognitoUserGroup string
	OIDCGroups       []string
}

// WorkteamSpec describes a private workteam to create.
type WorkteamSpec struct {
	Name                 string
	Description          string
	WorkforceName        string // empty means the "default" workforce
	Members              []MemberDefinition
	NotificationTopicARN string
	S3PresignSourceIP    string // Enabled or Disabled
	S3PresignVpcSourceIP string
	Tags                 []Tag
}

// Workteam is a private workteam.
type Workteam struct {
	Name                 string
	ARN                  string
	Description          string
	WorkforceName        string
	WorkforceARN         string
	SubDomain            string
	Members              []MemberDefinition
	NotificationTopicARN string
	S3PresignSourceIP    string
	S3PresignVpcSourceIP string
	ProductListingIDs    []string
	CreateDate           string
	LastUpdatedDate      string
}

// WorkteamUpdate describes an UpdateWorkteam request; a nil/empty field is
// unchanged.
type WorkteamUpdate struct {
	Name                 string
	Description          *string
	Members              []MemberDefinition
	NotificationTopicARN *string
	S3PresignSourceIP    *string
	S3PresignVpcSourceIP *string
}

// CognitoConfig identifies a Cognito user pool client of a workforce.
type CognitoConfig struct {
	UserPool string
	ClientID string
}

// OIDCConfig is the OpenID Connect identity provider of a workforce. The client
// secret is stored but never returned by Describe.
type OIDCConfig struct {
	AuthorizationEndpoint string
	ClientID              string
	ClientSecret          string
	Issuer                string
	JwksURI               string
	LogoutEndpoint        string
	TokenEndpoint         string
	UserInfoEndpoint      string
	Scope                 string
	ExtraParams           map[string]string
}

// WorkforceVPC is the VPC configuration of a workforce.
type WorkforceVPC struct {
	VPCID            string
	SecurityGroupIDs []string
	Subnets          []string
	VPCEndpointID    string
}

// WorkforceSpec describes a private workforce to create: Cognito or OIDC.
type WorkforceSpec struct {
	Name          string
	Cognito       *CognitoConfig
	OIDC          *OIDCConfig
	SourceCIDRs   []string
	IPAddressType string // ipv4 (default) or dualstack
	VPC           *WorkforceVPC
	Tags          []Tag
}

// Workforce is a private workforce.
type Workforce struct {
	Name            string
	ARN             string
	Status          string
	FailureReason   string
	SubDomain       string
	Cognito         *CognitoConfig
	OIDC            *OIDCConfig
	SourceCIDRs     []string
	IPAddressType   string
	VPC             *WorkforceVPC
	CreateDate      string
	LastUpdatedDate string
}

// WorkforceUpdate describes an UpdateWorkforce request; a nil field is unchanged.
type WorkforceUpdate struct {
	Name          string
	SourceCIDRs   []string
	OIDC          *OIDCConfig
	IPAddressType *string
	VPC           *WorkforceVPC
}

// LabelingJobForWorkteam is a labeling job as listed for a workteam.
type LabelingJobForWorkteam struct {
	JobName                string
	JobReferenceCode       string
	WorkRequesterAccountID string
	NumberOfHumanWorkers   int
	CreationTime           string
	HumanLabeled           int
	PendingHuman           int
	Total                  int
}

// SubscribedWorkteam is a vendor workteam subscribed through AWS Marketplace.
type SubscribedWorkteam struct {
	ARN                    string
	ListingID              string
	MarketplaceTitle       string
	MarketplaceDescription string
	SellerName             string
}

// workforceAPI covers private workforces, workteams and Ground Truth workteam
// queries.
type workforceAPI interface {
	CreateWorkforce(ctx context.Context, spec WorkforceSpec) (*Workforce, error)
	DescribeWorkforce(ctx context.Context, name string) (*Workforce, error)
	ListWorkforces(ctx context.Context, opts ListOptions) (ListPage[Workforce], error)
	UpdateWorkforce(ctx context.Context, update WorkforceUpdate) (*Workforce, error)
	DeleteWorkforce(ctx context.Context, name string) error

	CreateWorkteam(ctx context.Context, spec WorkteamSpec) (*Workteam, error)
	DescribeWorkteam(ctx context.Context, name string) (*Workteam, error)
	ListWorkteams(ctx context.Context, opts ListOptions) (ListPage[Workteam], error)
	UpdateWorkteam(ctx context.Context, update WorkteamUpdate) (*Workteam, error)
	DeleteWorkteam(ctx context.Context, name string) (bool, error)

	DescribeSubscribedWorkteam(ctx context.Context, arn string) (*SubscribedWorkteam, error)
	ListSubscribedWorkteams(ctx context.Context, opts ListOptions) (ListPage[SubscribedWorkteam], error)
	ListLabelingJobsForWorkteam(ctx context.Context, workteamARN string, opts ListOptions) (ListPage[LabelingJobForWorkteam], error)
}
