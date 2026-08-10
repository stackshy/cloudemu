package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// classifierData is a classifier plus its own lock.
type classifierData struct {
	classifier driver.Classifier
	mu         sync.RWMutex
}

// CreateClassifier creates a classifier, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateClassifier(_ context.Context, c driver.Classifier) error {
	if !validName(c.Name) {
		return invalidInput("classifier name %q is invalid", c.Name)
	}

	// Exactly one classifier kind (Grok/XML/Json/Csv) must be given.
	switch c.Kind {
	case "Grok", "XML", "Json", "Csv":
	default:
		return invalidInput("exactly one of Grok/XML/Json/Csv classifier must be specified")
	}

	now := m.now()
	c.CreationTime = now
	c.LastUpdated = now
	c.Version = 1
	stored := copyClassifier(c)

	if !m.classifiers.SetIfAbsent(c.Name, &classifierData{classifier: stored}) {
		return alreadyExists("Classifier already exists: %s", c.Name)
	}

	return nil
}

func (m *Mock) getClassifierData(name string) (*classifierData, error) {
	if !validName(name) {
		return nil, invalidInput("classifier name %q is invalid", name)
	}

	cd, ok := m.classifiers.Get(name)
	if !ok {
		return nil, entityNotFound("Classifier not found: %s", name)
	}

	return cd, nil
}

// GetClassifier returns a deep copy of a classifier.
func (m *Mock) GetClassifier(_ context.Context, name string) (*driver.Classifier, error) {
	cd, err := m.getClassifierData(name)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := copyClassifier(cd.classifier)

	return &out, nil
}

// UpdateClassifier replaces a classifier's definition.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateClassifier(_ context.Context, c driver.Classifier) error {
	cd, err := m.getClassifierData(c.Name)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	created := cd.classifier.CreationTime
	version := cd.classifier.Version
	cd.classifier = copyClassifier(c)
	cd.classifier.CreationTime = created
	cd.classifier.LastUpdated = m.now()
	cd.classifier.Version = version + 1

	return nil
}

// DeleteClassifier removes a classifier.
func (m *Mock) DeleteClassifier(_ context.Context, name string) error {
	if _, err := m.getClassifierData(name); err != nil {
		return err
	}

	m.classifiers.Delete(name)

	return nil
}

// GetClassifiers lists classifiers with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetClassifiers(
	_ context.Context, page driver.TablePagination,
) ([]driver.Classifier, string, error) {
	keys := sortedKeys(m.classifiers.Keys())
	all := make([]driver.Classifier, 0, len(keys))

	for _, key := range keys {
		cd, ok := m.classifiers.Get(key)
		if !ok {
			continue
		}

		cd.mu.RLock()
		all = append(all, copyClassifier(cd.classifier))
		cd.mu.RUnlock()
	}

	return paginate(all, page)
}
