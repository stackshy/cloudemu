package sfn

import (
	"context"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

func (m *Mock) PublishStateMachineVersion(
	_ context.Context, arn, description string,
) (versionArn string, created time.Time, err error) {
	sd, err := m.getSM(arn)
	if err != nil {
		return "", time.Time{}, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	now := m.now()
	versionArn = publishLocked(&sd.sm, description, now)

	return versionArn, now, nil
}

func (m *Mock) ListStateMachineVersions(_ context.Context, arn string) ([]driver.Version, error) {
	sd, err := m.getSM(arn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return append([]driver.Version(nil), sd.sm.PublishedVersions...), nil
}

// DeleteStateMachineVersion removes a published version. The version ARN is
// "<stateMachineArn>:<version>", so the parent state machine ARN is the ARN
// with the trailing ":<version>" segment stripped.
func (m *Mock) DeleteStateMachineVersion(_ context.Context, versionArn string) error {
	idx := strings.LastIndex(versionArn, ":")
	if idx < 0 {
		return invalidArn("%q is not a valid state machine version ARN", versionArn)
	}

	smArn := versionArn[:idx]

	sd, err := m.getSM(smArn)
	if err != nil {
		return err
	}

	// A version an alias still routes to can't be deleted — real SFN returns
	// ConflictException rather than silently orphaning the alias.
	if alias := m.aliasRoutingTo(versionArn); alias != "" {
		return conflict("state machine version %q is referenced by alias %q", versionArn, alias)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	kept := sd.sm.PublishedVersions[:0]

	for _, v := range sd.sm.PublishedVersions {
		if v.ARN != versionArn {
			kept = append(kept, v)
		}
	}

	sd.sm.PublishedVersions = kept
	if sd.sm.LatestVersionArn == versionArn {
		sd.sm.LatestVersionArn = ""
	}

	return nil
}

// aliasRoutingTo returns the ARN of an alias whose routing configuration
// references the given version ARN, or "" if none do.
func (m *Mock) aliasRoutingTo(versionArn string) string {
	for _, ad := range m.aliases.All() {
		ad.mu.RLock()
		for i := range ad.alias.Routing {
			if ad.alias.Routing[i].StateMachineVersionArn == versionArn {
				arn := ad.alias.ARN
				ad.mu.RUnlock()

				return arn
			}
		}
		ad.mu.RUnlock()
	}

	return ""
}

func (m *Mock) getAlias(arn string) (*aliasData, error) {
	if !validStateMachineARN(arn) {
		return nil, invalidArn("%q is not a valid state machine alias ARN", arn)
	}

	ad, ok := m.aliases.Get(arn)
	if !ok {
		return nil, resourceNotFound(arn)
	}

	return ad, nil
}

// smNameFromVersionARN derives the state machine name from a version ARN
// ("<stateMachineArn>:<version>").
func smNameFromVersionARN(versionArn string) string {
	idx := strings.LastIndex(versionArn, ":")
	if idx < 0 {
		return ""
	}

	return smNameFromARN(versionArn[:idx])
}

func (m *Mock) CreateStateMachineAlias(
	_ context.Context, name, description string, routing []driver.RouteEntry,
) (arn string, created time.Time, err error) {
	if name == "" {
		return "", time.Time{}, invalidName("alias name is required")
	}

	if len(routing) == 0 {
		return "", time.Time{}, invalidName("alias routing configuration is required")
	}

	smName := smNameFromVersionARN(routing[0].StateMachineVersionArn)
	if smName == "" {
		return "", time.Time{}, invalidArn("%q is not a valid state machine version ARN",
			routing[0].StateMachineVersionArn)
	}

	if err := m.validateRouting(smName, routing); err != nil {
		return "", time.Time{}, err
	}

	arn = m.aliasARN(smName, name)
	now := m.now()
	alias := driver.Alias{
		ARN: arn, Name: name, Description: description,
		Routing: append([]driver.RouteEntry(nil), routing...), CreationDate: now, UpdateDate: now,
	}

	// Alias names are unique per state machine; reject a duplicate atomically.
	if !m.aliases.SetIfAbsent(arn, &aliasData{alias: alias}) {
		return "", time.Time{}, conflict("state machine alias %q already exists", name)
	}

	return arn, now, nil
}

// maxAliasRoutes is SFN's cap on routing entries in a state-machine alias.
const maxAliasRoutes = 2

// totalRouteWeight is the required sum of routing weights in an alias.
const totalRouteWeight = 100

// validateRouting checks alias routing against real SFN rules: at most two
// entries, weights summing to 100, and every referenced version must exist on
// the same state machine.
func (m *Mock) validateRouting(smName string, routing []driver.RouteEntry) error {
	if len(routing) > maxAliasRoutes {
		return validationErr("an alias routing configuration may reference at most %d versions", maxAliasRoutes)
	}

	var sum int32

	for i := range routing {
		if smNameFromVersionARN(routing[i].StateMachineVersionArn) != smName {
			return validationErr("all routing versions must belong to the same state machine")
		}

		sd, err := m.getSM(m.smARN(smName))
		if err != nil {
			return err
		}

		if !versionExists(sd, routing[i].StateMachineVersionArn) {
			return resourceNotFound(routing[i].StateMachineVersionArn)
		}

		sum += routing[i].Weight
	}

	if sum != totalRouteWeight {
		return validationErr("routing weights must sum to %d, got %d", totalRouteWeight, sum)
	}

	return nil
}

// versionExists reports whether a published version with the given ARN exists on
// the state machine.
func versionExists(sd *smData, versionArn string) bool {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	for i := range sd.sm.PublishedVersions {
		if sd.sm.PublishedVersions[i].ARN == versionArn {
			return true
		}
	}

	return false
}

func (m *Mock) DescribeStateMachineAlias(_ context.Context, arn string) (*driver.Alias, error) {
	ad, err := m.getAlias(arn)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := ad.alias
	out.Routing = append([]driver.RouteEntry(nil), ad.alias.Routing...)

	return &out, nil
}

func (m *Mock) UpdateStateMachineAlias(
	_ context.Context, arn, description string, routing []driver.RouteEntry,
) (time.Time, error) {
	ad, err := m.getAlias(arn)
	if err != nil {
		return time.Time{}, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	if description != "" {
		ad.alias.Description = description
	}

	if len(routing) > 0 {
		ad.alias.Routing = append([]driver.RouteEntry(nil), routing...)
	}

	now := m.now()
	ad.alias.UpdateDate = now

	return now, nil
}

func (m *Mock) DeleteStateMachineAlias(_ context.Context, arn string) error {
	if !validStateMachineARN(arn) {
		return invalidArn("%q is not a valid state machine alias ARN", arn)
	}

	m.aliases.Delete(arn)

	return nil
}

func (m *Mock) ListStateMachineAliases(_ context.Context, stateMachineArn string) ([]driver.Alias, error) {
	if _, err := m.getSM(stateMachineArn); err != nil {
		return nil, err
	}

	smName := smNameFromARN(stateMachineArn)
	all := m.aliases.SortedValues()
	out := make([]driver.Alias, 0, len(all))

	for _, ad := range all {
		ad.mu.RLock()
		alias := ad.alias
		ad.mu.RUnlock()

		// The alias ARN embeds the owning state machine name as its 4th
		// colon-separated tail segment (stateMachine:<name>:<alias>).
		if aliasOwnerName(alias.ARN) != smName {
			continue
		}

		alias.Routing = append([]driver.RouteEntry(nil), alias.Routing...)
		out = append(out, alias)
	}

	return out, nil
}

// aliasOwnerName extracts the owning state machine name from an alias ARN of
// shape arn:aws:states:<region>:<account>:stateMachine:<name>:<alias>.
func aliasOwnerName(aliasArn string) string {
	seg := strings.SplitN(aliasArn, ":", arnParts)
	if len(seg) != arnParts {
		return ""
	}

	rest := strings.TrimPrefix(seg[5], "stateMachine:")

	name, _, found := strings.Cut(rest, ":")
	if !found {
		return ""
	}

	return name
}
