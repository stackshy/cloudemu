package cosmosaccount

import (
	"crypto/sha256"
	"encoding/base64"
)

// Key roles used to derive the four deterministic master keys an account
// exposes. Real Cosmos keys are opaque base64 blobs; the emulator derives them
// from the account name so ListKeys is stable across calls and every key is
// distinct.
const (
	rolePrimary           = "primary"
	roleSecondary         = "secondary"
	rolePrimaryReadonly   = "primary-readonly"
	roleSecondaryReadonly = "secondary-readonly"
)

// accountKey derives a stable, base64 pseudo-key for one role of an account.
func accountKey(name, role string) string {
	sum := sha256.Sum256([]byte(name + ":" + role))

	return base64.StdEncoding.EncodeToString(sum[:])
}

// listKeysResult builds the DatabaseAccountListKeysResult (all four keys).
func listKeysResult(name string) armListKeysResult {
	return armListKeysResult{
		PrimaryMasterKey:           accountKey(name, rolePrimary),
		SecondaryMasterKey:         accountKey(name, roleSecondary),
		PrimaryReadonlyMasterKey:   accountKey(name, rolePrimaryReadonly),
		SecondaryReadonlyMasterKey: accountKey(name, roleSecondaryReadonly),
	}
}

// readOnlyKeysResult builds the DatabaseAccountListReadOnlyKeysResult.
func readOnlyKeysResult(name string) armReadOnlyKeysResult {
	return armReadOnlyKeysResult{
		PrimaryReadonlyMasterKey:   accountKey(name, rolePrimaryReadonly),
		SecondaryReadonlyMasterKey: accountKey(name, roleSecondaryReadonly),
	}
}

// connectionStringsResult builds the four SQL-API connection strings, one per
// key kind, matching the DatabaseAccountListConnectionStringsResult shape. base
// is the emulator's scheme://host, so the embedded AccountEndpoint resolves back
// to the emulator (see documentEndpoint).
func connectionStringsResult(base, name string) armConnectionStringsResult {
	entry := func(desc, key, kind string) armConnectionString {
		return armConnectionString{
			ConnectionString: "AccountEndpoint=" + documentEndpoint(base, name) + ";AccountKey=" + key + ";",
			Description:      desc,
			KeyKind:          kind,
			Type:             "Sql",
		}
	}

	return armConnectionStringsResult{
		ConnectionStrings: []armConnectionString{
			entry("Primary SQL Connection String", accountKey(name, rolePrimary), "Primary"),
			entry("Secondary SQL Connection String", accountKey(name, roleSecondary), "Secondary"),
			entry("Primary Read-Only SQL Connection String", accountKey(name, rolePrimaryReadonly), "PrimaryReadonly"),
			entry("Secondary Read-Only SQL Connection String", accountKey(name, roleSecondaryReadonly), "SecondaryReadonly"),
		},
	}
}
