// Package driver defines the interface and types for the Amazon Location Service
// control-plane API (restJson1). It models the five Location resource types the
// Terraform AWS provider manages — maps, place indexes, route calculators,
// geofence collections and trackers — plus their resource tags.
//
// This is a control-plane-only surface. The Location data plane (geocoding,
// routing, geofence evaluation, device-position ingestion, map tiles) is out of
// scope: the emulator provisions and describes the resources so unmodified SDK,
// CLI and Terraform code manages them, but performs no real geospatial work.
//
// Every computed field a client or IaC tool reads back — each resource's ARN,
// CreateTime and UpdateTime — is minted once at create and stored, so repeated
// Describe/List reads never drift. An Update mutates only the requested fields
// and bumps UpdateTime; the ARN and CreateTime stay stable for the life of the
// resource.
package driver

import (
	"context"
	"time"
)

// Data-source values a place index or route calculator draws from.
const (
	DataSourceEsri     = "Esri"
	DataSourceHere     = "Here"
	DataSourceGrab     = "Grab"
	DataSourceGrabMaps = "GrabMaps"
)

// IntendedUse values for a place index data-source configuration. SingleUse is
// the default the real service applies when the caller omits it.
const (
	IntendedUseSingleUse  = "SingleUse"
	IntendedUseStorage    = "Storage"
	DefaultIntendedUse    = IntendedUseSingleUse
	DefaultPositionFilter = PositionFilteringTimeBased
)

// PositionFiltering values for a tracker. TimeBased is the service default.
const (
	PositionFilteringTimeBased     = "TimeBased"
	PositionFilteringDistanceBased = "DistanceBased"
	PositionFilteringAccuracyBased = "AccuracyBased"
)

// Meta is the state every Location resource shares: its name, its computed ARN,
// an optional description, the create/update timestamps and its tags. The ARN
// and CreateTime are minted once at create and never change; UpdateTime is
// bumped on every mutation.
type Meta struct {
	Name        string
	Arn         string
	Description string
	CreateTime  time.Time
	UpdateTime  time.Time
	Tags        map[string]string
}

// MapConfiguration is the immutable rendering configuration of a map. Style
// selects the map data and appearance and cannot be changed after create.
type MapConfiguration struct {
	Style         string   `json:"Style"`
	PoliticalView string   `json:"PoliticalView,omitempty"`
	CustomLayers  []string `json:"CustomLayers,omitempty"`
}

// DataSourceConfiguration is the place-index search configuration. IntendedUse
// is SingleUse or Storage; the service defaults it to SingleUse.
type DataSourceConfiguration struct {
	IntendedUse string `json:"IntendedUse,omitempty"`
}

// MapInfo is a map resource. Configuration and DataSource are fixed at create;
// DataSource is derived from the configured style.
type MapInfo struct {
	Meta
	Configuration MapConfiguration
	DataSource    string
	PricingPlan   string
}

// PlaceIndexInfo is a place-index resource.
type PlaceIndexInfo struct {
	Meta
	DataSource              string
	DataSourceConfiguration DataSourceConfiguration
	PricingPlan             string
}

// RouteCalculatorInfo is a route-calculator resource.
type RouteCalculatorInfo struct {
	Meta
	DataSource  string
	PricingPlan string
}

// GeofenceCollectionInfo is a geofence-collection resource. GeofenceCount is
// always zero: the geofence data plane is out of scope.
type GeofenceCollectionInfo struct {
	Meta
	KmsKeyID              string
	PricingPlan           string
	PricingPlanDataSource string
	GeofenceCount         int32
}

// TrackerInfo is a tracker resource.
type TrackerInfo struct {
	Meta
	KmsKeyID                      string
	PositionFiltering             string
	EventBridgeEnabled            bool
	KmsKeyEnableGeospatialQueries bool
	PricingPlan                   string
	PricingPlanDataSource         string
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// CreateMapInput is the input to CreateMap.
type CreateMapInput struct {
	MapName       string            `json:"MapName"`
	Configuration MapConfiguration  `json:"Configuration"`
	Description   string            `json:"Description"`
	PricingPlan   string            `json:"PricingPlan"`
	Tags          map[string]string `json:"Tags"`
}

// UpdateMapInput is the input to UpdateMap. A nil field is left unchanged; the
// map style is immutable and cannot be updated.
type UpdateMapInput struct {
	MapName     string  `json:"-"`
	Description *string `json:"Description"`
	PricingPlan *string `json:"PricingPlan"`
}

// CreatePlaceIndexInput is the input to CreatePlaceIndex.
type CreatePlaceIndexInput struct {
	IndexName               string                  `json:"IndexName"`
	DataSource              string                  `json:"DataSource"`
	DataSourceConfiguration DataSourceConfiguration `json:"DataSourceConfiguration"`
	Description             string                  `json:"Description"`
	PricingPlan             string                  `json:"PricingPlan"`
	Tags                    map[string]string       `json:"Tags"`
}

// UpdatePlaceIndexInput is the input to UpdatePlaceIndex.
type UpdatePlaceIndexInput struct {
	IndexName               string                   `json:"-"`
	DataSourceConfiguration *DataSourceConfiguration `json:"DataSourceConfiguration"`
	Description             *string                  `json:"Description"`
	PricingPlan             *string                  `json:"PricingPlan"`
}

// CreateRouteCalculatorInput is the input to CreateRouteCalculator.
type CreateRouteCalculatorInput struct {
	CalculatorName string            `json:"CalculatorName"`
	DataSource     string            `json:"DataSource"`
	Description    string            `json:"Description"`
	PricingPlan    string            `json:"PricingPlan"`
	Tags           map[string]string `json:"Tags"`
}

// UpdateRouteCalculatorInput is the input to UpdateRouteCalculator.
type UpdateRouteCalculatorInput struct {
	CalculatorName string  `json:"-"`
	Description    *string `json:"Description"`
	PricingPlan    *string `json:"PricingPlan"`
}

// CreateGeofenceCollectionInput is the input to CreateGeofenceCollection.
type CreateGeofenceCollectionInput struct {
	CollectionName        string            `json:"CollectionName"`
	Description           string            `json:"Description"`
	KmsKeyID              string            `json:"KmsKeyId"`
	PricingPlan           string            `json:"PricingPlan"`
	PricingPlanDataSource string            `json:"PricingPlanDataSource"`
	Tags                  map[string]string `json:"Tags"`
}

// UpdateGeofenceCollectionInput is the input to UpdateGeofenceCollection.
type UpdateGeofenceCollectionInput struct {
	CollectionName        string  `json:"-"`
	Description           *string `json:"Description"`
	PricingPlan           *string `json:"PricingPlan"`
	PricingPlanDataSource *string `json:"PricingPlanDataSource"`
}

// CreateTrackerInput is the input to CreateTracker.
type CreateTrackerInput struct {
	TrackerName                   string            `json:"TrackerName"`
	Description                   string            `json:"Description"`
	KmsKeyID                      string            `json:"KmsKeyId"`
	PositionFiltering             string            `json:"PositionFiltering"`
	EventBridgeEnabled            bool              `json:"EventBridgeEnabled"`
	KmsKeyEnableGeospatialQueries bool              `json:"KmsKeyEnableGeospatialQueries"`
	PricingPlan                   string            `json:"PricingPlan"`
	PricingPlanDataSource         string            `json:"PricingPlanDataSource"`
	Tags                          map[string]string `json:"Tags"`
}

// UpdateTrackerInput is the input to UpdateTracker.
type UpdateTrackerInput struct {
	TrackerName           string  `json:"-"`
	Description           *string `json:"Description"`
	PositionFiltering     *string `json:"PositionFiltering"`
	EventBridgeEnabled    *bool   `json:"EventBridgeEnabled"`
	PricingPlan           *string `json:"PricingPlan"`
	PricingPlanDataSource *string `json:"PricingPlanDataSource"`
}

// Location is the Amazon Location Service control-plane surface: maps, place
// indexes, route calculators, geofence collections, trackers and their tags.
type Location interface {
	CreateMap(ctx context.Context, in *CreateMapInput) (*MapInfo, error)
	DescribeMap(ctx context.Context, name string) (*MapInfo, error)
	UpdateMap(ctx context.Context, in *UpdateMapInput) (*MapInfo, error)
	DeleteMap(ctx context.Context, name string) error
	ListMaps(ctx context.Context, page Page) (maps []MapInfo, nextToken string, err error)

	CreatePlaceIndex(ctx context.Context, in *CreatePlaceIndexInput) (*PlaceIndexInfo, error)
	DescribePlaceIndex(ctx context.Context, name string) (*PlaceIndexInfo, error)
	UpdatePlaceIndex(ctx context.Context, in *UpdatePlaceIndexInput) (*PlaceIndexInfo, error)
	DeletePlaceIndex(ctx context.Context, name string) error
	ListPlaceIndexes(ctx context.Context, page Page) (indexes []PlaceIndexInfo, nextToken string, err error)

	CreateRouteCalculator(ctx context.Context, in *CreateRouteCalculatorInput) (*RouteCalculatorInfo, error)
	DescribeRouteCalculator(ctx context.Context, name string) (*RouteCalculatorInfo, error)
	UpdateRouteCalculator(ctx context.Context, in *UpdateRouteCalculatorInput) (*RouteCalculatorInfo, error)
	DeleteRouteCalculator(ctx context.Context, name string) error
	ListRouteCalculators(ctx context.Context, page Page) (calcs []RouteCalculatorInfo, nextToken string, err error)

	CreateGeofenceCollection(ctx context.Context, in *CreateGeofenceCollectionInput) (*GeofenceCollectionInfo, error)
	DescribeGeofenceCollection(ctx context.Context, name string) (*GeofenceCollectionInfo, error)
	UpdateGeofenceCollection(ctx context.Context, in *UpdateGeofenceCollectionInput) (*GeofenceCollectionInfo, error)
	DeleteGeofenceCollection(ctx context.Context, name string) error
	ListGeofenceCollections(ctx context.Context, page Page) (colls []GeofenceCollectionInfo, nextToken string, err error)

	CreateTracker(ctx context.Context, in *CreateTrackerInput) (*TrackerInfo, error)
	DescribeTracker(ctx context.Context, name string) (*TrackerInfo, error)
	UpdateTracker(ctx context.Context, in *UpdateTrackerInput) (*TrackerInfo, error)
	DeleteTracker(ctx context.Context, name string) error
	ListTrackers(ctx context.Context, page Page) (trackers []TrackerInfo, nextToken string, err error)

	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (tags map[string]string, err error)
}
