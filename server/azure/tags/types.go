package tags

// tagsBody is the ARM PUT body (armresources.TagsResource) and the shared shape
// of every tags-at-scope response. Only the properties block is writable on
// input; id/name/type are service-minted and ignored when present on a request.
type tagsBody struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Properties tagsProps `json:"properties"`
}

// tagsPatchBody is the ARM PATCH body (armresources.TagsPatchResource). The
// operation selects merge/replace/delete semantics against the current set.
type tagsPatchBody struct {
	Operation  string    `json:"operation"`
	Properties tagsProps `json:"properties"`
}

// tagsProps is the { tags: { k: v } } dictionary carried under properties.
type tagsProps struct {
	Tags map[string]string `json:"tags"`
}
