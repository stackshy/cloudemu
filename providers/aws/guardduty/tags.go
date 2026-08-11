package guardduty

import (
	"context"
	"encoding/json"
	"strings"
)

// arnPartCount is the number of colon-separated fields in a GuardDuty resource
// ARN (arn:aws:guardduty:<region>:<account>:<resource-path>).
const arnPartCount = 6

// GuardDuty ARN resource-path prefixes and per-detector child resource kinds.
const (
	segDetector = "detector"
	segPlan     = "malware-protection-plan"

	kindIPSet            = "ipset"
	kindThreatIntelSet   = "threatintelset"
	kindThreatEntitySet  = "threatentityset"
	kindTrustedEntitySet = "trustedentityset"
	kindFilter           = "filter"
)

// A GuardDuty resource ARN has the form
//
//	arn:aws:guardduty:<region>:<account>:<resource-path>
//
// where <resource-path> is one of:
//
//	detector/{id}
//	detector/{id}/ipset/{id}
//	detector/{id}/threatintelset/{id}
//	detector/{id}/threatentityset/{id}
//	detector/{id}/trustedentityset/{id}
//	detector/{id}/filter/{name}
//	malware-protection-plan/{id}
//
// parseGuardDutyARN splits an ARN into its resource path segments. It returns a
// BadRequestException for anything that is not a GuardDuty ARN.
func parseGuardDutyARN(arn string) (segments []string, err error) {
	parts := strings.SplitN(arn, ":", arnPartCount)
	if len(parts) != arnPartCount || parts[0] != "arn" || parts[2] != "guardduty" {
		return nil, badRequest("invalid GuardDuty resource ARN: %q", arn)
	}

	seg := strings.Split(parts[5], "/")
	if len(seg) == 0 || seg[0] == "" {
		return nil, badRequest("invalid GuardDuty resource ARN: %q", arn)
	}

	return seg, nil
}

// tagAccessor reads and atomically updates the tags of a resource identified by
// an ARN.
type tagAccessor struct {
	// read returns a deep copy of the resource's current tags, or false if the
	// resource does not exist.
	read func() (map[string]string, bool)
	// update applies mutate to the resource's tags atomically under the owning
	// resource's lock (read-modify-write in one hold, so concurrent tag updates
	// can't lose each other's writes), returning false if the resource is gone.
	update func(mutate func(map[string]string) map[string]string) bool
}

// resolveTagTarget maps an ARN to a tagAccessor for the owning resource, or a
// BadRequestException if the ARN does not name a known, taggable resource.
func (m *Mock) resolveTagTarget(arn string) (tagAccessor, error) {
	seg, err := parseGuardDutyARN(arn)
	if err != nil {
		return tagAccessor{}, err
	}

	switch {
	case seg[0] == segPlan && len(seg) == 2:
		return m.planTagAccessor(seg[1]), nil
	case seg[0] == segDetector && len(seg) == 2:
		return m.detectorTagAccessor(seg[1]), nil
	case seg[0] == segDetector && len(seg) == 4:
		return m.detectorChildTagAccessor(seg[1], seg[2], seg[3])
	default:
		return tagAccessor{}, badRequest("ARN does not identify a taggable GuardDuty resource: %q", arn)
	}
}

// planTagAccessor returns a tagAccessor for a malware protection plan. The
// memstore Update runs the mutate under the store lock, so read-modify-write is
// atomic.
func (m *Mock) planTagAccessor(planID string) tagAccessor {
	return tagAccessor{
		read: func() (map[string]string, bool) {
			p, ok := m.malwarePlans.Get(planID)
			if !ok {
				return nil, false
			}

			return copyTags(p.tags), true
		},
		update: func(mutate func(map[string]string) map[string]string) bool {
			return m.malwarePlans.Update(planID, func(p malwarePlanData) malwarePlanData {
				p.tags = mutate(copyTags(p.tags))

				return p
			})
		},
	}
}

// detectorTagAccessor returns a tagAccessor for a detector, holding the detector
// lock across the whole read-modify-write.
func (m *Mock) detectorTagAccessor(detectorID string) tagAccessor {
	return tagAccessor{
		read: func() (map[string]string, bool) {
			dd, ok := m.detectors.Get(detectorID)
			if !ok {
				return nil, false
			}

			dd.mu.RLock()
			defer dd.mu.RUnlock()

			return copyTags(dd.detector.Tags), true
		},
		update: func(mutate func(map[string]string) map[string]string) bool {
			dd, ok := m.detectors.Get(detectorID)
			if !ok {
				return false
			}

			dd.mu.Lock()
			defer dd.mu.Unlock()

			dd.detector.Tags = mutate(copyTags(dd.detector.Tags))

			return true
		},
	}
}

// detectorChildTagAccessor returns a tagAccessor for a per-detector child
// resource (ipset/threatintelset/threatentityset/trustedentityset/filter).
func (m *Mock) detectorChildTagAccessor(detectorID, kind, id string) (tagAccessor, error) {
	switch kind {
	case kindIPSet, kindThreatIntelSet, kindThreatEntitySet, kindTrustedEntitySet, kindFilter:
		return tagAccessor{
			read: func() (map[string]string, bool) { return m.readChildTags(detectorID, kind, id) },
			update: func(mutate func(map[string]string) map[string]string) bool {
				return m.updateChildTags(detectorID, kind, id, mutate)
			},
		}, nil
	default:
		return tagAccessor{}, badRequest("ARN does not identify a taggable GuardDuty resource type: %q", kind)
	}
}

// readChildTags reads a deep copy of a child resource's tags under the detector
// lock.
//
//nolint:gocyclo // one arm per taggable per-detector child resource type; large by API design.
func (m *Mock) readChildTags(detectorID, kind, id string) (map[string]string, bool) {
	dd, ok := m.detectors.Get(detectorID)
	if !ok {
		return nil, false
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	switch kind {
	case kindIPSet:
		if r, found := dd.ipSets[id]; found {
			return copyTags(r.Tags), true
		}
	case kindThreatIntelSet:
		if r, found := dd.threatIS[id]; found {
			return copyTags(r.Tags), true
		}
	case kindThreatEntitySet:
		if r, found := dd.threatES[id]; found {
			return copyTags(r.Tags), true
		}
	case kindTrustedEntitySet:
		if r, found := dd.trustES[id]; found {
			return copyTags(r.Tags), true
		}
	case kindFilter:
		if r, found := dd.filters[id]; found {
			return copyTags(r.Tags), true
		}
	}

	return nil, false
}

// updateChildTags applies mutate to a child resource's tags atomically under the
// detector lock (read current, mutate, write back in one hold), so concurrent
// tag updates cannot lose each other's writes.
//
//nolint:gocyclo // one arm per taggable per-detector child resource type; large by API design.
func (m *Mock) updateChildTags(detectorID, kind, id string, mutate func(map[string]string) map[string]string) bool {
	dd, ok := m.detectors.Get(detectorID)
	if !ok {
		return false
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	switch kind {
	case kindIPSet:
		if r, found := dd.ipSets[id]; found {
			r.Tags = mutate(copyTags(r.Tags))
			dd.ipSets[id] = r

			return true
		}
	case kindThreatIntelSet:
		if r, found := dd.threatIS[id]; found {
			r.Tags = mutate(copyTags(r.Tags))
			dd.threatIS[id] = r

			return true
		}
	case kindThreatEntitySet:
		if r, found := dd.threatES[id]; found {
			r.Tags = mutate(copyTags(r.Tags))
			dd.threatES[id] = r

			return true
		}
	case kindTrustedEntitySet:
		if r, found := dd.trustES[id]; found {
			r.Tags = mutate(copyTags(r.Tags))
			dd.trustES[id] = r

			return true
		}
	case kindFilter:
		if r, found := dd.filters[id]; found {
			r.Tags = mutate(copyTags(r.Tags))
			dd.filters[id] = r

			return true
		}
	}

	return false
}

// ListTagsForResource returns a deep copy of the tags on the resource an ARN
// names. An ARN that does not resolve yields ResourceNotFoundException.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (json.RawMessage, error) {
	acc, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return nil, err
	}

	tags, ok := acc.read()
	if !ok {
		return nil, badRequest("resource not found: %s", resourceARN)
	}

	if tags == nil {
		tags = map[string]string{}
	}

	return json.Marshal(map[string]any{"tags": tags})
}

// tagResourceRequest is the TagResource body.
type tagResourceRequest struct {
	Tags map[string]string `json:"tags"`
}

// TagResource merges the supplied tags into the resource an ARN names.
func (m *Mock) TagResource(_ context.Context, resourceARN string, body json.RawMessage) (json.RawMessage, error) {
	var req tagResourceRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	if len(req.Tags) == 0 {
		return nil, badRequest("tags is required")
	}

	acc, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return nil, err
	}

	ok := acc.update(func(current map[string]string) map[string]string {
		if current == nil {
			current = map[string]string{}
		}

		for k, v := range req.Tags {
			current[k] = v
		}

		return current
	})
	if !ok {
		return nil, badRequest("resource not found: %s", resourceARN)
	}

	return json.Marshal(map[string]any{})
}

// UntagResource removes the supplied tag keys from the resource an ARN names.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) (json.RawMessage, error) {
	acc, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return nil, err
	}

	ok := acc.update(func(current map[string]string) map[string]string {
		for _, k := range tagKeys {
			delete(current, k)
		}

		return current
	})
	if !ok {
		return nil, badRequest("resource not found: %s", resourceARN)
	}

	return json.Marshal(map[string]any{})
}
