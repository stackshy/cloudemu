package rds

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// validEngines is the set of Engine values RDS accepts on CreateDBInstance,
// mirroring the documented enum. Neptune and DocumentDB share the RDS
// query-protocol wire in cloudemu, so their engine names are accepted here too.
// AWS rejects any other value with InvalidParameterValue.
var validEngines = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"aurora-mysql":          {},
	"aurora-postgresql":     {},
	"custom-oracle-ee":      {},
	"custom-oracle-ee-cdb":  {},
	"custom-oracle-se2":     {},
	"custom-oracle-se2-cdb": {},
	"custom-sqlserver-ee":   {},
	"custom-sqlserver-se":   {},
	"custom-sqlserver-web":  {},
	"custom-sqlserver-dev":  {},
	"db2-ae":                {},
	"db2-ce":                {},
	"db2-se":                {},
	"mariadb":               {},
	"mysql":                 {},
	"oracle-ee":             {},
	"oracle-ee-cdb":         {},
	"oracle-se2":            {},
	"oracle-se2-cdb":        {},
	"postgres":              {},
	"sqlserver-dev-ee":      {},
	"sqlserver-ee":          {},
	"sqlserver-se":          {},
	"sqlserver-ex":          {},
	"sqlserver-web":         {},
	engineNeptune:           {},
	engineDocDB:             {},
}

// instanceClassFamilies is a curated set of the DB instance-class families RDS
// offers (db.<family>.<size>). The full class enum is region- and
// engine-dependent, so — like the engine-default parameter tables — this is a
// representative set broad enough to accept real classes while rejecting a
// clearly bogus family. Aurora Serverless v2 uses the db.serverless class,
// handled separately.
var instanceClassFamilies = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"t2": {}, "t3": {}, "t4g": {},
	"m1": {}, "m3": {}, "m4": {}, "m5": {}, "m5d": {}, "m6g": {}, "m6gd": {},
	"m6i": {}, "m6id": {}, "m7g": {}, "m7i": {}, "m8g": {},
	"r3": {}, "r4": {}, "r5": {}, "r5b": {}, "r5d": {}, "r6g": {}, "r6gd": {},
	"r6i": {}, "r6id": {}, "r7g": {}, "r7i": {}, "r8g": {},
	"x1": {}, "x1e": {}, "x2g": {}, "x2idn": {}, "x2iedn": {}, "x2iezn": {},
	"z1d": {},
}

// instanceClassSizes is a curated set of the size suffixes RDS instance classes
// use. Combined with instanceClassFamilies it validates the db.<family>.<size>
// shape without hard-coding every region/engine-specific class.
var instanceClassSizes = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"micro": {}, "small": {}, "medium": {}, "large": {}, "xlarge": {},
	"2xlarge": {}, "4xlarge": {}, "6xlarge": {}, "8xlarge": {}, "9xlarge": {},
	"12xlarge": {}, "16xlarge": {}, "18xlarge": {}, "24xlarge": {}, "32xlarge": {},
	"48xlarge": {}, "metal": {},
}

// validateEngine rejects an Engine value outside the accepted enum, matching
// real RDS which fails CreateDBInstance with InvalidParameterValue.
func validateEngine(engine string) error {
	if _, ok := validEngines[strings.ToLower(engine)]; !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "Invalid DB engine: %s", engine)
	}

	return nil
}

// validateInstanceClass rejects a malformed or unknown DBInstanceClass. A class
// is db.<family>.<size> (or db.serverless for Aurora Serverless v2); an empty
// class defaults later and is not validated here. Real RDS rejects an invalid
// class with InvalidParameterValue.
func validateInstanceClass(class string) error {
	if class == "" {
		return nil
	}

	if class == "db.serverless" {
		return nil
	}

	if instanceClassIsValid(class) {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument, "Invalid DB instance class: %s", class)
}

// instanceClassIsValid reports whether class is a db.<family>.<size> string
// whose family and size are both recognized.
func instanceClassIsValid(class string) bool {
	const classParts = 3

	parts := strings.Split(class, ".")
	if len(parts) != classParts || parts[0] != "db" {
		return false
	}

	if _, ok := instanceClassFamilies[parts[1]]; !ok {
		return false
	}

	_, ok := instanceClassSizes[parts[2]]

	return ok
}
