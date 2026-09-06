package appsync

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// API-key validity bounds and defaults, per AppSync. Expiry is floored to the
// hour; a caller-supplied expiry must fall within [now+1d, now+365d].
const (
	apiKeyDefaultValidity = 7 * 24 * time.Hour
	apiKeyMinValidity     = 24 * time.Hour
	apiKeyMaxValidity     = 365 * 24 * time.Hour
	apiKeyDeleteGrace     = 60 * 24 * time.Hour
)

// CreateAPIKey mints an API key whose expiry is computed once via the clock and
// floored to the hour, then never recomputed on a read.
func (m *Mock) CreateAPIKey(_ context.Context, in *driver.CreateAPIKeyInput) (*driver.APIKey, error) {
	ad, err := m.getAPI(in.APIID)
	if err != nil {
		return nil, err
	}

	expires, deletes, err := m.computeExpiry(in.Expires)
	if err != nil {
		return nil, err
	}

	key := driver.APIKey{
		ID:          idgen.GenerateID("da2-"),
		Description: in.Description,
		Expires:     expires,
		Deletes:     deletes,
	}

	ad.mu.Lock()
	ad.apiKeys[key.ID] = key
	ad.mu.Unlock()

	return &key, nil
}

// ListAPIKeys returns a deterministic, deep-copied page of an API's keys,
// ordered by id. Stored expiry values are returned as-is (never recomputed).
func (m *Mock) ListAPIKeys(_ context.Context, apiID string, page driver.Page) ([]driver.APIKey, string, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, "", err
	}

	ad.mu.RLock()
	all := sortedAPIKeys(ad.apiKeys)
	ad.mu.RUnlock()

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}

// UpdateAPIKey updates a key's description and, when supplied, its expiry
// (re-validated and re-floored). Omitted fields keep their existing values.
func (m *Mock) UpdateAPIKey(_ context.Context, in *driver.UpdateAPIKeyInput) (*driver.APIKey, error) {
	ad, err := m.getAPI(in.APIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	key, ok := ad.apiKeys[in.ID]
	if !ok {
		return nil, notFound("API key %s not found", in.ID)
	}

	if in.Description != nil {
		key.Description = *in.Description
	}

	if in.Expires != 0 {
		expires, deletes, cerr := m.computeExpiry(in.Expires)
		if cerr != nil {
			return nil, cerr
		}

		key.Expires = expires
		key.Deletes = deletes
	}

	ad.apiKeys[in.ID] = key

	return &key, nil
}

// DeleteAPIKey removes a key from an API.
func (m *Mock) DeleteAPIKey(_ context.Context, apiID, id string) error {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if _, ok := ad.apiKeys[id]; !ok {
		return notFound("API key %s not found", id)
	}

	delete(ad.apiKeys, id)

	return nil
}

// computeExpiry resolves an API-key expiry (epoch seconds) and its deletion
// time. A zero request selects the default 7-day validity; a supplied value
// must fall within [now+1d, now+365d] or APIKeyValidityOutOfBoundsException is
// returned. The result is floored to the hour.
func (m *Mock) computeExpiry(reqExpires int64) (expires, deletes int64, err error) {
	now := m.now()

	var exp time.Time
	if reqExpires == 0 {
		exp = now.Add(apiKeyDefaultValidity)
	} else {
		exp = time.Unix(reqExpires, 0).UTC()

		validity := exp.Sub(now)
		if validity < apiKeyMinValidity || validity > apiKeyMaxValidity {
			return 0, 0, apiKeyValidityOutOfBounds(
				"API key expiration must be between 1 and 365 days from now")
		}
	}

	floored := exp.Truncate(time.Hour)

	return floored.Unix(), floored.Add(apiKeyDeleteGrace).Unix(), nil
}

func sortedAPIKeys(m map[string]driver.APIKey) []driver.APIKey {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	out := make([]driver.APIKey, 0, len(m))
	for _, id := range ids {
		out = append(out, m[id])
	}

	return out
}
