package route53resolver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/route53resolver/driver"
)

func outpostNotFound(id string) error {
	return errors.Newf(errors.NotFound, "outpost resolver %q not found", id)
}

func cloneOutpost(o *driver.OutpostResolver) driver.OutpostResolver { return *o }

func (m *Mock) CreateOutpostResolver(
	_ context.Context, in *driver.CreateOutpostResolverInput,
) (*driver.OutpostResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("rslvr-op-")
	o := &driver.OutpostResolver{
		ID:                    id,
		ARN:                   m.arn("outpost-resolver/" + id),
		Name:                  in.Name,
		CreatorRequestID:      in.CreatorRequestID,
		OutpostARN:            in.OutpostARN,
		PreferredInstanceType: in.PreferredInstanceType,
		InstanceCount:         in.InstanceCount,
		Status:                statusOperational,
		CreatedAt:             m.now(),
		ModifiedAt:            m.now(),
	}
	m.outposts.Set(id, o)

	if len(in.Tags) > 0 {
		m.tags.Set(o.ARN, copyTags(in.Tags))
	}

	out := cloneOutpost(o)

	return &out, nil
}

func (m *Mock) GetOutpostResolver(_ context.Context, id string) (*driver.OutpostResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.outposts.Get(id)
	if !ok {
		return nil, outpostNotFound(id)
	}

	out := cloneOutpost(o)

	return &out, nil
}

func (m *Mock) UpdateOutpostResolver(
	_ context.Context, in *driver.UpdateOutpostResolverInput,
) (*driver.OutpostResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.outposts.Get(in.ID)
	if !ok {
		return nil, outpostNotFound(in.ID)
	}

	if in.Name != "" {
		o.Name = in.Name
	}

	if in.PreferredInstanceType != "" {
		o.PreferredInstanceType = in.PreferredInstanceType
	}

	if in.InstanceCount != 0 {
		o.InstanceCount = in.InstanceCount
	}

	o.ModifiedAt = m.now()

	out := cloneOutpost(o)

	return &out, nil
}

func (m *Mock) DeleteOutpostResolver(_ context.Context, id string) (*driver.OutpostResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.outposts.Get(id)
	if !ok {
		return nil, outpostNotFound(id)
	}

	m.outposts.Delete(id)
	m.tags.Delete(o.ARN)

	out := cloneOutpost(o)
	out.Status = statusDeleting

	return &out, nil
}

func (m *Mock) ListOutpostResolvers(_ context.Context) ([]driver.OutpostResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return sortedValues(m.outposts.All(), cloneOutpost), nil
}
