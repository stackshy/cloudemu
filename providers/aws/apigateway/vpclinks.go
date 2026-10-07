package apigateway

import (
	"context"
	"regexp"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

const (
	msgVpcLinkNotFound = "Invalid VPC link identifier specified"
	msgVpcLinkName     = "VPC link name is required"
	msgVpcLinkTargets  = "A VPC link needs exactly one Network Load Balancer target ARN"
	msgVpcLinkTarget   = "Invalid target ARN: must be a Network Load Balancer ARN"
	vpcLinkAvailable   = "AVAILABLE"
)

var nlbARNPattern = regexp.MustCompile(
	`^arn:aws[a-z-]*:elasticloadbalancing:[a-z0-9-]+:[0-9]{12}:loadbalancer/net/[A-Za-z0-9-]+/[A-Za-z0-9]+$`)

// CreateVpcLink creates a VPC link to a Network Load Balancer.
func (m *Mock) CreateVpcLink(_ context.Context, in *driver.CreateVpcLinkInput) (*driver.VpcLink, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgVpcLinkName)
	}

	if len(in.TargetARNs) != 1 {
		return nil, cerrors.New(cerrors.InvalidArgument, msgVpcLinkTargets)
	}

	if !nlbARNPattern.MatchString(in.TargetARNs[0]) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgVpcLinkTarget)
	}

	link := &driver.VpcLink{
		ID: genShortID(), Name: in.Name, Description: in.Description, TargetARNs: copyStrSlice(in.TargetARNs),
		Status: vpcLinkAvailable, Tags: copyStrMap(in.Tags),
	}

	m.regionMu.Lock()
	m.vpcLinks[link.ID] = link
	m.regionMu.Unlock()

	out := copyVpcLink(link)

	return &out, nil
}

// GetVpcLink returns one VPC link.
func (m *Mock) GetVpcLink(_ context.Context, id string) (*driver.VpcLink, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	link, ok := m.vpcLinks[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgVpcLinkNotFound)
	}

	out := copyVpcLink(link)

	return &out, nil
}

// GetVpcLinks lists VPC links ordered by name then id.
//
//nolint:dupl // the same list-sort-page shape as the sibling collections by design
func (m *Mock) GetVpcLinks(_ context.Context, page driver.PageInput) (*driver.VpcLinkPage, error) {
	m.regionMu.RLock()

	all := make([]driver.VpcLink, 0, len(m.vpcLinks))
	for _, l := range m.vpcLinks {
		all = append(all, copyVpcLink(l))
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, page)
	if err != nil {
		return nil, err
	}

	return &driver.VpcLinkPage{Items: items, Position: next}, nil
}

// UpdateVpcLink patches /name and /description.
func (m *Mock) UpdateVpcLink(_ context.Context, id string, ops []driver.PatchOperation) (*driver.VpcLink, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	link, ok := m.vpcLinks[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgVpcLinkNotFound)
	}

	upd := copyVpcLink(link)

	for _, op := range ops {
		switch op.Path {
		case pathName:
			upd.Name = op.Value
		case pathDescription:
			upd.Description = op.Value
		default:
			return nil, invalidPatchPath(op, pathName, pathDescription)
		}
	}

	if upd.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, msgVpcLinkName)
	}

	*link = upd
	out := copyVpcLink(link)

	return &out, nil
}

// DeleteVpcLink removes a VPC link unless an integration still uses it.
func (m *Mock) DeleteVpcLink(_ context.Context, id string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.vpcLinks[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgVpcLinkNotFound)
	}

	if m.vpcLinkInUse(id) {
		return cerrors.New(cerrors.FailedPrecondition, "The VPC link is in use by one or more integrations")
	}

	delete(m.vpcLinks, id)

	return nil
}

// vpcLinkInUse reports whether any live integration references the link.
func (m *Mock) vpcLinkInUse(id string) bool {
	for _, ad := range m.apis.All() {
		ad.mu.RLock()
		used := false

		for _, res := range ad.resources {
			for _, mth := range res.Methods {
				if mth.Integration != nil && mth.Integration.ConnectionID == id {
					used = true
				}
			}
		}

		ad.mu.RUnlock()

		if used {
			return true
		}
	}

	return false
}

func copyVpcLink(l *driver.VpcLink) driver.VpcLink {
	out := *l
	out.TargetARNs = copyStrSlice(l.TargetARNs)
	out.Tags = copyStrMap(l.Tags)

	return out
}
