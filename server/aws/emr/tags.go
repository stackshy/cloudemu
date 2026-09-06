package emr

import (
	"context"
	"net/http"
)

// addTagsInput mirrors the SDK AddTagsInput.
type addTagsInput struct {
	ResourceID *string    `json:"ResourceId"`
	Tags       []tagInput `json:"Tags"`
}

// removeTagsInput mirrors the SDK RemoveTagsInput.
type removeTagsInput struct {
	ResourceID *string  `json:"ResourceId"`
	TagKeys    []string `json:"TagKeys"`
}

// addTags upserts tags on a cluster by key (AddTags). terraform-provider-aws
// calls this to reconcile tag changes on an existing cluster.
func (s *store) addTags(clusterID string, tags []tagInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return notFound(clusterID)
	}

	for _, t := range tags {
		c.setTag(deref(t.Key), deref(t.Value))
	}

	return nil
}

// removeTags deletes tags from a cluster by key (RemoveTags).
func (s *store) removeTags(clusterID string, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return notFound(clusterID)
	}

	for _, k := range keys {
		c.deleteTag(k)
	}

	return nil
}

// setTag upserts a single tag, replacing the value when the key already exists.
func (c *cluster) setTag(key, value string) {
	for i := range c.tags {
		if c.tags[i].key == key {
			c.tags[i].value = value

			return
		}
	}

	c.tags = append(c.tags, tag{key: key, value: value})
}

// deleteTag removes a tag by key, if present.
func (c *cluster) deleteTag(key string) {
	for i := range c.tags {
		if c.tags[i].key == key {
			c.tags = append(c.tags[:i], c.tags[i+1:]...)

			return
		}
	}
}

// addTagsHandler handles AddTags.
func (h *Handler) addTagsHandler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *addTagsInput) (any, error) {
		if err := h.store.addTags(deref(in.ResourceID), in.Tags); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// removeTagsHandler handles RemoveTags.
func (h *Handler) removeTagsHandler(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *removeTagsInput) (any, error) {
		if err := h.store.removeTags(deref(in.ResourceID), in.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
