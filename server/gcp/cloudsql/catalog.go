package cloudsql

import "net/http"

// Cloud SQL exposes two static reference catalogs — machine tiers and database
// flags. Real Cloud SQL returns hundreds of region-specific entries; the mock
// serves a small representative set so SDK clients that enumerate them get a
// well-formed, non-empty response.

type tier struct {
	Kind      string   `json:"kind"`
	Tier      string   `json:"tier"`
	RAM       int64    `json:"RAM,string"`
	DiskQuota int64    `json:"DiskQuota,string"`
	Region    []string `json:"region"`
}

type tiersList struct {
	Kind  string `json:"kind"`
	Items []tier `json:"items"`
}

type flag struct {
	Kind                string   `json:"kind"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	AppliesTo           []string `json:"appliesTo"`
	AllowedStringValues []string `json:"allowedStringValues,omitempty"`
	MinValue            int64    `json:"minValue,omitempty,string"`
	MaxValue            int64    `json:"maxValue,omitempty,string"`
	RequiresRestart     bool     `json:"requiresRestart"`
}

type flagsList struct {
	Kind  string `json:"kind"`
	Items []flag `json:"items"`
}

const gib = 1 << 30

var catalogRegions = []string{"us-central1", "us-east1", "europe-west1"} //nolint:gochecknoglobals // static catalog

// serveTiers handles GET /v1/projects/{p}/tiers.
func serveTiers(w http.ResponseWriter, r *http.Request, _ *sqlPath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, tiersList{
		Kind: "sql#tiersList",
		Items: []tier{
			{Kind: "sql#tier", Tier: "db-f1-micro", RAM: 614 * (1 << 20), DiskQuota: 3072 * gib, Region: catalogRegions},
			{Kind: "sql#tier", Tier: "db-g1-small", RAM: 1740 * (1 << 20), DiskQuota: 3072 * gib, Region: catalogRegions},
			{Kind: "sql#tier", Tier: "db-custom-1-3840", RAM: 3840 * (1 << 20), DiskQuota: 65536 * gib, Region: catalogRegions},
			{Kind: "sql#tier", Tier: "db-custom-2-7680", RAM: 7680 * (1 << 20), DiskQuota: 65536 * gib, Region: catalogRegions},
		},
	})
}

// serveFlags handles GET /v1/flags.
func serveFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	mysqlPg := []string{"MYSQL_8_0", "POSTGRES_15"}

	writeJSON(w, http.StatusOK, flagsList{
		Kind: "sql#flagsList",
		Items: []flag{
			{
				Kind: "sql#flag", Name: "max_connections", Type: "INTEGER",
				AppliesTo: mysqlPg, MinValue: 1, MaxValue: 262143, RequiresRestart: true,
			},
			{
				Kind: "sql#flag", Name: "slow_query_log", Type: "STRING",
				AppliesTo: []string{"MYSQL_8_0"}, AllowedStringValues: []string{"on", "off"}, RequiresRestart: false,
			},
			{
				Kind: "sql#flag", Name: "log_min_duration_statement", Type: "INTEGER",
				AppliesTo: []string{"POSTGRES_15"}, MinValue: -1, MaxValue: 2147483647, RequiresRestart: false,
			},
		},
	})
}
