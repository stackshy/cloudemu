package cache

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
)

var _ driver.RedisChildren = (*Mock)(nil)

// defaultMaintenanceWindow is the window real Azure stores when a patch
// schedule entry omits maintenanceWindow.
const defaultMaintenanceWindow = "PT5H"

const maxStartHour = 23

// patchDays are the dayOfWeek values the Redis REST spec allows.
func patchDays() []string {
	return []string{
		"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "Everyday", "Weekend",
	}
}

func cacheNotFound(name string) error {
	return errors.Newf(errors.NotFound, "cache %q not found", name)
}

// SetPatchSchedule replaces the cache's patch schedule.
func (m *Mock) SetPatchSchedule(_ context.Context, cache string, entries []driver.PatchScheduleEntry) (bool, error) {
	normalized, err := normalizePatchEntries(entries)
	if err != nil {
		return false, err
	}

	var created bool

	ok := m.caches.Update(cache, func(cd *cacheData) *cacheData {
		updated := *cd
		created = cd.patch == nil
		updated.patch = normalized

		return &updated
	})
	if !ok {
		return false, cacheNotFound(cache)
	}

	return created, nil
}

// normalizePatchEntries validates entries and returns a copy with the real
// defaults filled in. Day names are matched case-insensitively and stored in
// their canonical form.
func normalizePatchEntries(entries []driver.PatchScheduleEntry) ([]driver.PatchScheduleEntry, error) {
	if len(entries) == 0 {
		return nil, errors.New(errors.InvalidArgument, "scheduleEntries must contain at least one entry")
	}

	out := make([]driver.PatchScheduleEntry, len(entries))

	for i, e := range entries {
		day := canonicalPatchDay(e.DayOfWeek)
		if day == "" {
			return nil, errors.Newf(errors.InvalidArgument, "dayOfWeek %q is not a valid day", e.DayOfWeek)
		}

		if e.StartHourUTC < 0 || e.StartHourUTC > maxStartHour {
			return nil, errors.Newf(errors.InvalidArgument, "startHourUtc %d must be between 0 and 23", e.StartHourUTC)
		}

		e.DayOfWeek = day
		if e.MaintenanceWindow == "" {
			e.MaintenanceWindow = defaultMaintenanceWindow
		}

		out[i] = e
	}

	return out, nil
}

func canonicalPatchDay(day string) string {
	for _, d := range patchDays() {
		if strings.EqualFold(d, day) {
			return d
		}
	}

	return ""
}

// GetPatchSchedule returns a copy of the cache's patch schedule.
func (m *Mock) GetPatchSchedule(_ context.Context, cache string) ([]driver.PatchScheduleEntry, error) {
	cd, ok := m.caches.Get(cache)
	if !ok {
		return nil, cacheNotFound(cache)
	}

	if cd.patch == nil {
		return nil, errors.Newf(errors.NotFound, "cache %q has no patch schedule", cache)
	}

	return slices.Clone(cd.patch), nil
}

// DeletePatchSchedule removes the cache's patch schedule.
func (m *Mock) DeletePatchSchedule(_ context.Context, cache string) (bool, error) {
	var existed bool

	ok := m.caches.Update(cache, func(cd *cacheData) *cacheData {
		existed = cd.patch != nil
		updated := *cd
		updated.patch = nil

		return &updated
	})
	if !ok {
		return false, cacheNotFound(cache)
	}

	return existed, nil
}

// PutFirewallRule creates or replaces a firewall rule.
func (m *Mock) PutFirewallRule(_ context.Context, cache string, rule driver.FirewallRule) (bool, error) {
	if err := validateFirewallRule(&rule); err != nil {
		return false, err
	}

	var created bool

	ok := m.caches.Update(cache, func(cd *cacheData) *cacheData {
		updated := *cd
		key, existed := firewallRuleKey(cd.fw, rule.Name)
		created = !existed

		updated.fw = maps.Clone(cd.fw)
		if updated.fw == nil {
			updated.fw = make(map[string]driver.FirewallRule)
		}

		if existed {
			// Azure rule names are case-insensitive: a PUT under another
			// casing replaces the stored rule and keeps its original name.
			rule.Name = key
		}

		updated.fw[key] = rule

		return &updated
	})
	if !ok {
		return false, cacheNotFound(cache)
	}

	return created, nil
}

func validateFirewallRule(rule *driver.FirewallRule) error {
	if rule.Name == "" {
		return errors.New(errors.InvalidArgument, "firewall rule name is required")
	}

	start, err := netip.ParseAddr(rule.StartIP)
	if err != nil || !start.Is4() {
		return errors.Newf(errors.InvalidArgument, "startIP %q is not a valid IPv4 address", rule.StartIP)
	}

	end, err := netip.ParseAddr(rule.EndIP)
	if err != nil || !end.Is4() {
		return errors.Newf(errors.InvalidArgument, "endIP %q is not a valid IPv4 address", rule.EndIP)
	}

	if end.Less(start) {
		return errors.Newf(errors.InvalidArgument, "startIP %s must not be greater than endIP %s", rule.StartIP, rule.EndIP)
	}

	return nil
}

// GetFirewallRule returns one firewall rule.
func (m *Mock) GetFirewallRule(_ context.Context, cache, name string) (driver.FirewallRule, error) {
	cd, ok := m.caches.Get(cache)
	if !ok {
		return driver.FirewallRule{}, cacheNotFound(cache)
	}

	key, ok := firewallRuleKey(cd.fw, name)
	if !ok {
		return driver.FirewallRule{}, errors.Newf(errors.NotFound, "firewall rule %q not found", name)
	}

	return cd.fw[key], nil
}

// firewallRuleKey returns the stored key matching name case-insensitively,
// as Azure treats firewall rule names.
func firewallRuleKey(fw map[string]driver.FirewallRule, name string) (string, bool) {
	if _, ok := fw[name]; ok {
		return name, true
	}

	for key := range fw {
		if strings.EqualFold(key, name) {
			return key, true
		}
	}

	return name, false
}

// DeleteFirewallRule removes a firewall rule.
func (m *Mock) DeleteFirewallRule(_ context.Context, cache, name string) (bool, error) {
	var existed bool

	ok := m.caches.Update(cache, func(cd *cacheData) *cacheData {
		var key string

		key, existed = firewallRuleKey(cd.fw, name)
		if !existed {
			return cd
		}

		updated := *cd
		updated.fw = maps.Clone(cd.fw)
		delete(updated.fw, key)

		return &updated
	})
	if !ok {
		return false, cacheNotFound(cache)
	}

	return existed, nil
}

// ListFirewallRules returns the cache's firewall rules sorted by name.
func (m *Mock) ListFirewallRules(_ context.Context, cache string) ([]driver.FirewallRule, error) {
	cd, ok := m.caches.Get(cache)
	if !ok {
		return nil, cacheNotFound(cache)
	}

	out := make([]driver.FirewallRule, 0, len(cd.fw))
	for _, name := range slices.Sorted(maps.Keys(cd.fw)) {
		out = append(out, cd.fw[name])
	}

	return out, nil
}
