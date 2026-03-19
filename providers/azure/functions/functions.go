// Package functions provides an in-memory mock implementation of Azure Functions.
package functions

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// Compile-time check that Mock implements driver.Serverless.
var _ driver.Serverless = (*Mock)(nil)

type funcData struct {
	info    driver.FunctionInfo
	handler driver.HandlerFunc
}

// Mock is an in-memory mock implementation of Azure Functions.
type Mock struct {
	funcs      *memstore.Store[funcData]
	opts       *config.Options
	handlersMu sync.RWMutex
	handlers   map[string]driver.HandlerFunc
}

// New creates a new Azure Functions mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		funcs:    memstore.New[funcData](),
		opts:     opts,
		handlers: make(map[string]driver.HandlerFunc),
	}
}

// CreateFunction creates a new Azure Function.
func (m *Mock) CreateFunction(_ context.Context, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	if _, ok := m.funcs.Get(cfg.Name); ok {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function %s already exists", cfg.Name)
	}
	resourceID := idgen.AzureID(m.opts.AccountID, "cloudemu-rg", "Microsoft.Web", "sites", cfg.Name)
	info := driver.FunctionInfo{
		Name: cfg.Name, ARN: resourceID, Runtime: cfg.Runtime, Handler: cfg.Handler,
		Memory: cfg.Memory, Timeout: cfg.Timeout, State: "Active",
		Environment: cfg.Environment, Tags: cfg.Tags,
		LastModified: time.Now().UTC().Format(time.RFC3339),
	}
	m.handlersMu.RLock()
	h := m.handlers[cfg.Name]
	m.handlersMu.RUnlock()
	m.funcs.Set(cfg.Name, funcData{info: info, handler: h})
	result := info
	return &result, nil
}

// DeleteFunction deletes an Azure Function by name.
func (m *Mock) DeleteFunction(_ context.Context, name string) error {
	if !m.funcs.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}
	m.funcs.Delete(name)
	return nil
}

// GetFunction retrieves an Azure Function by name.
func (m *Mock) GetFunction(_ context.Context, name string) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}
	info := fd.info
	return &info, nil
}

// ListFunctions lists all Azure Functions.
func (m *Mock) ListFunctions(_ context.Context) ([]driver.FunctionInfo, error) {
	all := m.funcs.All()
	infos := make([]driver.FunctionInfo, 0, len(all))
	for _, fd := range all {
		infos = append(infos, fd.info)
	}
	return infos, nil
}

// UpdateFunction updates an existing Azure Function's configuration.
func (m *Mock) UpdateFunction(_ context.Context, name string, cfg driver.FunctionConfig) (*driver.FunctionInfo, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}
	info := fd.info
	if cfg.Runtime != "" {
		info.Runtime = cfg.Runtime
	}
	if cfg.Handler != "" {
		info.Handler = cfg.Handler
	}
	if cfg.Memory != 0 {
		info.Memory = cfg.Memory
	}
	if cfg.Timeout != 0 {
		info.Timeout = cfg.Timeout
	}
	if cfg.Environment != nil {
		info.Environment = cfg.Environment
	}
	if cfg.Tags != nil {
		info.Tags = cfg.Tags
	}
	info.LastModified = time.Now().UTC().Format(time.RFC3339)
	m.funcs.Set(name, funcData{info: info, handler: fd.handler})
	result := info
	return &result, nil
}

// Invoke invokes an Azure Function with the given input.
func (m *Mock) Invoke(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	fd, ok := m.funcs.Get(input.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", input.FunctionName)
	}
	h := fd.handler
	if h == nil {
		m.handlersMu.RLock()
		h = m.handlers[input.FunctionName]
		m.handlersMu.RUnlock()
	}
	if h == nil {
		return &driver.InvokeOutput{StatusCode: 500, Error: "no handler registered"}, nil
	}
	payload, err := h(ctx, input.Payload)
	if err != nil {
		return &driver.InvokeOutput{StatusCode: 500, Error: err.Error()}, nil
	}
	return &driver.InvokeOutput{StatusCode: 200, Payload: payload}, nil
}

// RegisterHandler registers a handler function for an Azure Function by name.
func (m *Mock) RegisterHandler(name string, handler driver.HandlerFunc) {
	m.handlersMu.Lock()
	m.handlers[name] = handler
	m.handlersMu.Unlock()
	if fd, ok := m.funcs.Get(name); ok {
		fd.handler = handler
		m.funcs.Set(name, fd)
	}
}
