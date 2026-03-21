// Package ecr provides an in-memory mock implementation of AWS Elastic Container Registry.
package ecr

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/containerregistry/driver"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

const (
	defaultMediaType     = "application/vnd.docker.distribution.manifest.v2+json"
	mutableTag           = "MUTABLE"
	immutableTag         = "IMMUTABLE"
	scanStatusComplete   = "COMPLETE"
	digestHashFormatByte = 8
)

// Compile-time check that Mock implements driver.ContainerRegistry.
var _ driver.ContainerRegistry = (*Mock)(nil)

type imageData struct {
	detail driver.ImageDetail
	layers []driver.LayerInfo
}

type repoData struct {
	info          driver.Repository
	images        *memstore.Store[*imageData]
	scans         *memstore.Store[*driver.ScanResult]
	policy        *driver.LifecyclePolicy
	scanOnPush    bool
	tagMutability string
}

// Mock is an in-memory mock implementation of the AWS ECR service.
type Mock struct {
	repos      *memstore.Store[*repoData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/ECR", MetricName: metricName, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new ECR mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		repos: memstore.New[*repoData](),
		opts:  opts,
	}
}

// CreateRepository creates a new ECR repository.
func (m *Mock) CreateRepository(_ context.Context, cfg driver.RepositoryConfig) (*driver.Repository, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "repository name is required")
	}

	if m.repos.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "repository %q already exists", cfg.Name)
	}

	mutability := cfg.ImageTagMutability
	if mutability == "" {
		mutability = mutableTag
	}

	tags := copyTags(cfg.Tags)
	uri := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", m.opts.AccountID, m.opts.Region, cfg.Name)

	info := driver.Repository{
		Name:       cfg.Name,
		URI:        uri,
		CreatedAt:  m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:       tags,
		ImageCount: 0,
	}

	rd := &repoData{
		info:          info,
		images:        memstore.New[*imageData](),
		scans:         memstore.New[*driver.ScanResult](),
		scanOnPush:    cfg.ImageScanOnPush,
		tagMutability: mutability,
	}

	m.repos.Set(cfg.Name, rd)

	result := info

	return &result, nil
}

// DeleteRepository deletes an ECR repository.
func (m *Mock) DeleteRepository(_ context.Context, name string, force bool) error {
	rd, ok := m.repos.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", name)
	}

	if !force && rd.images.Len() > 0 {
		return errors.Newf(errors.FailedPrecondition, "repository %q is not empty; use force to delete", name)
	}

	m.repos.Delete(name)

	return nil
}

// GetRepository retrieves information about an ECR repository.
func (m *Mock) GetRepository(_ context.Context, name string) (*driver.Repository, error) {
	rd, ok := m.repos.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", name)
	}

	result := rd.info
	result.ImageCount = rd.images.Len()

	return &result, nil
}

// ListRepositories lists all ECR repositories.
func (m *Mock) ListRepositories(_ context.Context) ([]driver.Repository, error) {
	all := m.repos.All()
	repos := make([]driver.Repository, 0, len(all))

	for _, rd := range all {
		repo := rd.info
		repo.ImageCount = rd.images.Len()
		repos = append(repos, repo)
	}

	return repos, nil
}

// PutImage pushes an image manifest to an ECR repository.
func (m *Mock) PutImage(_ context.Context, manifest *driver.ImageManifest) (*driver.ImageDetail, error) {
	rd, ok := m.repos.Get(manifest.Repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", manifest.Repository)
	}

	if err := checkTagMutability(rd, manifest.Tag); err != nil {
		return nil, err
	}

	digest := resolveDigest(manifest, m.opts.Clock.Now())
	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)
	mediaType := resolveMediaType(manifest.MediaType)

	detail := driver.ImageDetail{
		RegistryID: m.opts.AccountID,
		Repository: manifest.Repository,
		Digest:     digest,
		Tags:       []string{manifest.Tag},
		SizeBytes:  manifest.SizeBytes,
		PushedAt:   now,
		MediaType:  mediaType,
	}

	img := &imageData{detail: detail, layers: manifest.Layers}
	rd.images.Set(digest, img)

	if manifest.Tag != "" {
		updateTagIndex(rd, digest, manifest.Tag)
	}

	rd.info.ImageCount = rd.images.Len()

	if rd.scanOnPush {
		autoScan(rd, digest, manifest.Repository, m.opts.Clock.Now())
	}

	m.emitMetric("ImagePushCount", 1, map[string]string{"RepositoryName": manifest.Repository})

	result := detail

	return &result, nil
}

// GetImage retrieves image details by repository and reference.
func (m *Mock) GetImage(_ context.Context, repository, reference string) (*driver.ImageDetail, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, reference)
	if img == nil {
		return nil, errors.Newf(errors.NotFound, "image %q not found in repository %q", reference, repository)
	}

	m.emitMetric("ImagePullCount", 1, map[string]string{"RepositoryName": repository})

	result := img.detail

	return &result, nil
}

// ListImages lists all images in an ECR repository.
func (m *Mock) ListImages(_ context.Context, repository string) ([]driver.ImageDetail, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	all := rd.images.All()
	images := make([]driver.ImageDetail, 0, len(all))

	for _, img := range all {
		images = append(images, img.detail)
	}

	return images, nil
}

// DeleteImage deletes an image from an ECR repository by reference.
func (m *Mock) DeleteImage(_ context.Context, repository, reference string) error {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, reference)
	if img == nil {
		return errors.Newf(errors.NotFound, "image %q not found in repository %q", reference, repository)
	}

	rd.images.Delete(img.detail.Digest)
	rd.scans.Delete(img.detail.Digest)
	rd.info.ImageCount = rd.images.Len()

	return nil
}

// TagImage adds a new tag to an existing image in an ECR repository.
func (m *Mock) TagImage(_ context.Context, repository, sourceRef, targetTag string) error {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, sourceRef)
	if img == nil {
		return errors.Newf(errors.NotFound, "image %q not found in repository %q", sourceRef, repository)
	}

	if err := checkTagMutability(rd, targetTag); err != nil {
		return err
	}

	updateTagIndex(rd, img.detail.Digest, targetTag)

	return nil
}

// PutLifecyclePolicy sets a lifecycle policy on an ECR repository.
func (m *Mock) PutLifecyclePolicy(_ context.Context, repository string, policy driver.LifecyclePolicy) error {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	policyCopy := copyLifecyclePolicy(policy)
	rd.policy = &policyCopy

	return nil
}

// GetLifecyclePolicy retrieves the lifecycle policy for an ECR repository.
func (m *Mock) GetLifecyclePolicy(_ context.Context, repository string) (*driver.LifecyclePolicy, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	if rd.policy == nil {
		return nil, errors.Newf(errors.NotFound, "no lifecycle policy for repository %q", repository)
	}

	result := copyLifecyclePolicy(*rd.policy)

	return &result, nil
}

// EvaluateLifecyclePolicy evaluates the lifecycle policy and returns digests to expire.
func (m *Mock) EvaluateLifecyclePolicy(_ context.Context, repository string) ([]string, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	if rd.policy == nil {
		return []string{}, nil
	}

	return evaluateRules(rd, m.opts.Clock.Now()), nil
}

// StartImageScan starts a vulnerability scan on an image in an ECR repository.
func (m *Mock) StartImageScan(_ context.Context, repository, reference string) (*driver.ScanResult, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, reference)
	if img == nil {
		return nil, errors.Newf(errors.NotFound, "image %q not found in repository %q", reference, repository)
	}

	scan := generateScanResult(repository, img.detail.Digest, m.opts.Clock.Now())
	rd.scans.Set(img.detail.Digest, scan)

	result := *scan

	return &result, nil
}

// GetImageScanResults retrieves scan results for an image in an ECR repository.
func (m *Mock) GetImageScanResults(
	_ context.Context, repository, reference string,
) (*driver.ScanResult, error) {
	rd, ok := m.repos.Get(repository)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	img := findImage(rd, reference)
	if img == nil {
		return nil, errors.Newf(errors.NotFound, "image %q not found in repository %q", reference, repository)
	}

	scan, ok := rd.scans.Get(img.detail.Digest)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "no scan results for image %q in repository %q", reference, repository)
	}

	result := *scan

	return &result, nil
}

// findImage locates an image by tag or digest.
func findImage(rd *repoData, reference string) *imageData {
	// Try direct digest lookup first.
	if img, ok := rd.images.Get(reference); ok {
		return img
	}

	// Search by tag.
	all := rd.images.All()
	for _, img := range all {
		for _, tag := range img.detail.Tags {
			if tag == reference {
				return img
			}
		}
	}

	return nil
}

// checkTagMutability validates that a tag can be overwritten.
func checkTagMutability(rd *repoData, tag string) error {
	if tag == "" || rd.tagMutability != immutableTag {
		return nil
	}

	all := rd.images.All()
	for _, img := range all {
		for _, t := range img.detail.Tags {
			if t == tag {
				return errors.Newf(
					errors.FailedPrecondition,
					"tag %q already exists and repository has IMMUTABLE tag mutability",
					tag,
				)
			}
		}
	}

	return nil
}

// resolveDigest generates a digest from the manifest if not provided.
func resolveDigest(manifest *driver.ImageManifest, now time.Time) string {
	if manifest.Digest != "" {
		return manifest.Digest
	}

	input := fmt.Sprintf("%s:%s:%s", manifest.Repository, manifest.Tag, now.String())
	hash := sha256.Sum256([]byte(input))

	return fmt.Sprintf("sha256:%x", hash[:digestHashFormatByte])
}

// updateTagIndex removes a tag from any existing image and adds it to the target digest.
func updateTagIndex(rd *repoData, digest, tag string) {
	// Remove tag from any other image.
	all := rd.images.All()
	for d, img := range all {
		if d == digest {
			continue
		}

		filtered := removeTag(img.detail.Tags, tag)
		if len(filtered) != len(img.detail.Tags) {
			img.detail.Tags = filtered
			rd.images.Set(d, img)
		}
	}

	// Add tag to target image.
	if img, ok := rd.images.Get(digest); ok {
		if !hasTag(img.detail.Tags, tag) {
			img.detail.Tags = append(img.detail.Tags, tag)
			rd.images.Set(digest, img)
		}
	}
}

// autoScan creates a scan result automatically when scanOnPush is enabled.
func autoScan(rd *repoData, digest, repository string, now time.Time) {
	scan := generateScanResult(repository, digest, now)
	rd.scans.Set(digest, scan)
}

// generateScanResult produces deterministic scan findings based on the digest hash.
func generateScanResult(repository, digest string, now time.Time) *driver.ScanResult {
	hash := sha256.Sum256([]byte(digest))

	findings := map[string]int{
		"CRITICAL":      int(hash[0]) % 3,
		"HIGH":          int(hash[1]) % 5,
		"MEDIUM":        int(hash[2]) % 10,
		"LOW":           int(hash[3]) % 15,
		"INFORMATIONAL": int(hash[4]) % 8,
	}

	return &driver.ScanResult{
		Repository:    repository,
		Digest:        digest,
		Status:        scanStatusComplete,
		FindingCounts: findings,
		CompletedAt:   now.UTC().Format(time.RFC3339),
	}
}

// evaluateRules applies lifecycle rules sorted by priority and returns digests to expire.
func evaluateRules(rd *repoData, now time.Time) []string {
	rules := rd.policy.Rules
	sorted := make([]driver.LifecycleRule, len(rules))
	copy(sorted, rules)

	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })

	expired := make(map[string]bool)

	for idx := range sorted {
		digests := evaluateSingleRule(rd, &sorted[idx], now)
		for _, d := range digests {
			expired[d] = true
		}
	}

	result := make([]string, 0, len(expired))
	for d := range expired {
		result = append(result, d)
	}

	return result
}

// evaluateSingleRule evaluates one lifecycle rule and returns matching digests.
func evaluateSingleRule(rd *repoData, rule *driver.LifecycleRule, now time.Time) []string {
	images := collectMatchingImages(rd, rule)

	sort.Slice(images, func(i, j int) bool { return images[i].detail.PushedAt < images[j].detail.PushedAt })

	switch rule.CountType {
	case "imageCountMoreThan":
		return expireByCount(images, rule.CountValue)
	case "sinceImagePushed":
		return expireBySince(images, rule.CountValue, now)
	default:
		return nil
	}
}

// collectMatchingImages returns images matching the rule's tag status and pattern.
func collectMatchingImages(rd *repoData, rule *driver.LifecycleRule) []*imageData {
	all := rd.images.All()

	var matched []*imageData

	for _, img := range all {
		if matchesTagRule(img, rule) {
			matched = append(matched, img)
		}
	}

	return matched
}

// matchesTagRule checks if an image matches the rule's tag filter.
func matchesTagRule(img *imageData, rule *driver.LifecycleRule) bool {
	switch rule.TagStatus {
	case "untagged":
		return len(img.detail.Tags) == 0
	case "tagged":
		return len(img.detail.Tags) > 0 && matchTagPattern(img.detail.Tags, rule.TagPattern)
	default: // "any"
		return true
	}
}

// matchTagPattern checks if any tag matches the given glob pattern.
func matchTagPattern(tags []string, pattern string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	for _, tag := range tags {
		if matched, err := path.Match(pattern, tag); err == nil && matched {
			return true
		}
	}

	return false
}

// expireByCount returns digests of excess images beyond the count threshold.
func expireByCount(images []*imageData, maxCount int) []string {
	if len(images) <= maxCount {
		return nil
	}

	excess := len(images) - maxCount
	result := make([]string, 0, excess)

	for i := range excess {
		result = append(result, images[i].detail.Digest)
	}

	return result
}

// expireBySince returns digests of images pushed more than N days ago.
func expireBySince(images []*imageData, days int, now time.Time) []string {
	cutoff := now.AddDate(0, 0, -days)

	var result []string

	for _, img := range images {
		pushedAt, err := time.Parse(time.RFC3339, img.detail.PushedAt)
		if err != nil {
			continue
		}

		if pushedAt.Before(cutoff) {
			result = append(result, img.detail.Digest)
		}
	}

	return result
}

func resolveMediaType(mediaType string) string {
	if mediaType != "" {
		return mediaType
	}

	return defaultMediaType
}

func copyTags(src map[string]string) map[string]string {
	tags := make(map[string]string, len(src))
	for k, v := range src {
		tags[k] = v
	}

	return tags
}

func copyLifecyclePolicy(p driver.LifecyclePolicy) driver.LifecyclePolicy {
	rules := make([]driver.LifecycleRule, len(p.Rules))
	copy(rules, p.Rules)

	return driver.LifecyclePolicy{Rules: rules}
}

func removeTag(tags []string, tag string) []string {
	result := make([]string, 0, len(tags))

	for _, t := range tags {
		if t != tag {
			result = append(result, t)
		}
	}

	return result
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}

	return false
}
