package inject

import "sync"

// Injector manages error injection per service/operation.
type Injector struct {
	mu    sync.RWMutex
	rules map[string]*Rule
}

// Rule is an error injection rule.
type Rule struct {
	Error  error
	Policy Policy
}

// NewInjector creates a new Injector.
func NewInjector() *Injector {
	return &Injector{rules: make(map[string]*Rule)}
}

// key creates a lookup key from service and operation.
func key(service, operation string) string {
	return service + ":" + operation
}

// Set registers an error injection rule.
func (inj *Injector) Set(service, operation string, err error, policy Policy) {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	inj.rules[key(service, operation)] = &Rule{Error: err, Policy: policy}
}

// Remove removes an error injection rule.
func (inj *Injector) Remove(service, operation string) {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	delete(inj.rules, key(service, operation))
}

// Check checks if an error should be injected. Returns nil if no injection.
func (inj *Injector) Check(service, operation string) error {
	inj.mu.RLock()
	defer inj.mu.RUnlock()

	rule, ok := inj.rules[key(service, operation)]
	if !ok {
		return nil
	}

	if rule.Policy.ShouldInject() {
		return rule.Error
	}

	return nil
}

// Reset removes all rules.
func (inj *Injector) Reset() {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	inj.rules = make(map[string]*Rule)
}
