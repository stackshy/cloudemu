package driver

import "context"

// FeatureGroupConfig describes a (BigQuery-backed) feature group.
type FeatureGroupConfig struct {
	Location       string
	FeatureGroupID string
	Description    string
	BigQueryURI    string
}

// FeatureGroup is a feature group.
type FeatureGroup struct {
	Name        string // projects/{p}/locations/{l}/featureGroups/{id}
	Description string
	BigQueryURI string
	CreateTime  string
}

// Feature is a feature within a group.
type Feature struct {
	Name        string // .../featureGroups/{g}/features/{id}
	Description string
	CreateTime  string
}

// FeatureOnlineStoreConfig describes an online store.
type FeatureOnlineStoreConfig struct {
	Location             string
	FeatureOnlineStoreID string
}

// FeatureOnlineStore serves online feature reads.
type FeatureOnlineStore struct {
	Name       string // projects/{p}/locations/{l}/featureOnlineStores/{id}
	State      string
	CreateTime string
}

// FeatureViewConfig describes a feature view in an online store.
type FeatureViewConfig struct {
	Parent        string // online store resource name
	FeatureViewID string
	BigQueryURI   string
}

// FeatureView maps source data into an online store.
type FeatureView struct {
	Name        string // .../featureOnlineStores/{s}/featureViews/{id}
	BigQueryURI string
	CreateTime  string
}

// FeatureNameValue is one feature name/value in an online read.
type FeatureNameValue struct {
	Name  string
	Value string
}

// featureStoreAPI covers the BigQuery-backed Feature Store: groups, features,
// online stores, views, and the fetchFeatureValues data plane.
type featureStoreAPI interface {
	CreateFeatureGroup(ctx context.Context, cfg FeatureGroupConfig) (*Operation, *FeatureGroup, error)
	GetFeatureGroup(ctx context.Context, name string) (*FeatureGroup, error)
	ListFeatureGroups(ctx context.Context, location string) ([]FeatureGroup, error)
	DeleteFeatureGroup(ctx context.Context, name string) (*Operation, error)

	CreateFeature(ctx context.Context, parent, featureID, description string) (*Operation, *Feature, error)
	GetFeature(ctx context.Context, name string) (*Feature, error)
	ListFeatures(ctx context.Context, parent string) ([]Feature, error)
	DeleteFeature(ctx context.Context, name string) (*Operation, error)

	CreateFeatureOnlineStore(ctx context.Context, cfg FeatureOnlineStoreConfig) (*Operation, *FeatureOnlineStore, error)
	GetFeatureOnlineStore(ctx context.Context, name string) (*FeatureOnlineStore, error)
	ListFeatureOnlineStores(ctx context.Context, location string) ([]FeatureOnlineStore, error)
	DeleteFeatureOnlineStore(ctx context.Context, name string) (*Operation, error)

	CreateFeatureView(ctx context.Context, cfg FeatureViewConfig) (*Operation, *FeatureView, error)
	GetFeatureView(ctx context.Context, name string) (*FeatureView, error)
	ListFeatureViews(ctx context.Context, parent string) ([]FeatureView, error)
	DeleteFeatureView(ctx context.Context, name string) (*Operation, error)
	FetchFeatureValues(ctx context.Context, featureView, entityID string) ([]FeatureNameValue, error)
}
