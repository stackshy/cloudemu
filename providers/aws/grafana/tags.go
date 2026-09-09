package grafana

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// TagResource adds or overwrites tags on a workspace.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	id, err := workspaceIDFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateWorkspaceTags(id, resourceArn, func(t map[string]string) {
		for k, v := range tags {
			t[k] = v
		}
	})
}

// UntagResource removes tags by key from a workspace.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	id, err := workspaceIDFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateWorkspaceTags(id, resourceArn, func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	})
}

// ListTagsForResource returns a copy of a workspace's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	id, err := workspaceIDFromARN(resourceArn)
	if err != nil {
		return nil, err
	}

	w, ok := m.workspaces.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	return copyTags(w.Tags), nil
}

// arnFields is the number of colon-separated fields in a Grafana ARN
// (arn:aws:grafana:{region}:{acct}:/workspaces/{id}); the sixth is the resource.
const arnFields = 6

// workspaceIDFromARN extracts the workspace id from a Grafana resource ARN of
// the form arn:aws:grafana:{region}:{acct}:/workspaces/{id}.
func workspaceIDFromARN(resourceArn string) (string, error) {
	if !strings.Contains(resourceArn, arnMarker) {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	// The resource part is the sixth colon-separated field; it carries a leading
	// slash and contains no further colon, so a SplitN of arnFields isolates it.
	parts := strings.SplitN(resourceArn, ":", arnFields)
	if len(parts) < arnFields {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	resource := strings.TrimPrefix(parts[arnFields-1], "/")

	const wsPrefix = "workspaces/"
	if !strings.HasPrefix(resource, wsPrefix) {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	id := strings.TrimPrefix(resource, wsPrefix)
	if id == "" {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	return id, nil
}

func (m *Mock) updateWorkspaceTags(id, arn string, mutate func(map[string]string)) error {
	ok := m.workspaces.Update(id, func(w driver.Workspace) driver.Workspace {
		if w.Tags == nil {
			w.Tags = map[string]string{}
		}

		mutate(w.Tags)

		return w
	})
	if !ok {
		return notFound(arn)
	}

	return nil
}
