package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

//nolint:dupl // child accessors differ per resource type by design
func (m *Mock) experienceChild() *child[driver.Experience] {
	return &child[driver.Experience]{
		kind: "experience", store: m.experiences,
		idOf:     func(e *driver.Experience) string { return e.ID },
		indexOf:  func(e *driver.Experience) string { return e.IndexID },
		tokenOf:  func(e *driver.Experience) string { return e.ClientToken },
		statusOf: func(e *driver.Experience) string { return e.Status },
	}
}

func (m *Mock) viewExperience(e *driver.Experience) driver.Experience {
	out := *e
	out.Configuration = copyRaw(e.Configuration)
	out.Endpoints = append([]driver.ExperienceEndpoint(nil), e.Endpoints...)
	out.Status = m.settleStatus(childKey(e.IndexID, e.ID), e.Status)

	return out
}

// experienceEndpoint is the home URL a search experience is served at. The
// emulator hosts no experience UI, so the URL is synthesized from the id and
// region and is not reachable.
func (m *Mock) experienceEndpoint(id string) []driver.ExperienceEndpoint {
	return []driver.ExperienceEndpoint{{
		Endpoint:     "https://" + id + ".experience.kendra." + m.opts.Region + ".amazonaws.com/",
		EndpointType: "HOME",
	}}
}

// CreateExperience creates a search experience for an index. A repeated
// ClientToken returns the first experience.
func (m *Mock) CreateExperience(_ context.Context, in *driver.CreateExperienceInput) (*driver.Experience, error) {
	if err := validateIndexID(in.IndexID); err != nil {
		return nil, err
	}

	if err := validateName(in.Name, maxIndexNameLen); err != nil {
		return nil, err
	}

	if in.RoleArn != "" {
		if err := validateRoleArn(in.RoleArn); err != nil {
			return nil, err
		}
	}

	if err := validateCommon(in.ClientToken, in.Description, nil); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return nil, err
	}

	if existing, ok := childByToken(m.experienceChild(), in.IndexID, in.ClientToken); ok {
		out := m.viewExperience(&existing)

		return &out, nil
	}

	id := newUUID()
	now := m.now()

	e := driver.Experience{
		ID: id, IndexID: in.IndexID, Name: in.Name, Description: in.Description, RoleArn: in.RoleArn,
		Configuration: copyRaw(in.Configuration), Status: driver.ChildStatusActive,
		Endpoints: m.experienceEndpoint(id), ClientToken: in.ClientToken, CreatedAt: now, UpdatedAt: now,
	}

	m.experiences.Set(childKey(in.IndexID, id), e)
	m.beginSettle(childKey(in.IndexID, id), driver.ChildStatusCreating)

	out := m.viewExperience(&e)

	return &out, nil
}

// DescribeExperience returns an experience by index and id.
func (m *Mock) DescribeExperience(_ context.Context, indexID, id string) (*driver.Experience, error) {
	e, err := childGet(m, m.experienceChild(), indexID, id)
	if err != nil {
		return nil, err
	}

	out := m.viewExperience(&e)

	return &out, nil
}

// UpdateExperience applies the supplied fields, leaving omitted ones unchanged.
func (m *Mock) UpdateExperience(_ context.Context, in *driver.UpdateExperienceInput) error {
	if err := validateOptionalChild(in.Name, in.RoleArn, in.Description); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childRequireActive(m, m.experienceChild(), in.IndexID, in.ID); err != nil {
		return err
	}

	key := childKey(in.IndexID, in.ID)

	m.experiences.Update(key, func(e driver.Experience) driver.Experience {
		applyExperienceUpdate(&e, in)
		e.UpdatedAt = m.now()

		return e
	})
	// No settle window: ExperienceStatus has no UPDATING value.

	return nil
}

func applyExperienceUpdate(e *driver.Experience, in *driver.UpdateExperienceInput) {
	if in.Name != nil {
		e.Name = *in.Name
	}

	if in.Description != nil {
		e.Description = *in.Description
	}

	if in.RoleArn != nil {
		e.RoleArn = *in.RoleArn
	}

	if in.Configuration != nil {
		e.Configuration = copyRaw(in.Configuration)
	}
}

// validateOptionalChild checks the name, role and description of an update, each
// only when supplied.
func validateOptionalChild(name, roleArn, description *string) error {
	if name != nil {
		if err := validateName(*name, maxIndexNameLen); err != nil {
			return err
		}
	}

	if roleArn != nil && *roleArn != "" {
		if err := validateRoleArn(*roleArn); err != nil {
			return err
		}
	}

	if description != nil && len(*description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	return nil
}

// ListExperiences returns a deterministic page of an index's experiences.
func (m *Mock) ListExperiences(
	_ context.Context, indexID string, page driver.Page,
) (experiences []driver.Experience, nextToken string, err error) {
	items, next, err := childList(m, m.experienceChild(), indexID, page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.Experience, 0, len(items))
	for i := range items {
		out = append(out, m.viewExperience(&items[i]))
	}

	return out, next, nil
}

// DeleteExperience removes an experience.
func (m *Mock) DeleteExperience(_ context.Context, indexID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := childRequireActive(m, m.experienceChild(), indexID, id); err != nil {
		return err
	}

	m.experiences.Delete(childKey(indexID, id))
	m.settling.Clear(childKey(indexID, id))

	return nil
}
