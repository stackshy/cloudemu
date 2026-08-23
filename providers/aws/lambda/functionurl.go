package lambda

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Function URL defaults applied when the request omits them, matching real
// Lambda.
const (
	defaultInvokeMode = "BUFFERED"
	defaultAuthType   = "NONE"
)

// CreateFunctionURLConfig provisions the (single) Function URL for a function.
//
//nolint:gocritic // hugeParam: matches the functionURLManager interface signature.
func (m *Mock) CreateFunctionURLConfig(_ context.Context, cfg driver.FunctionURLConfig) (*driver.FunctionURLConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if fd.urlConfig != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "function url config for %s already exists", cfg.FunctionName)
	}

	now := m.opts.Clock.Now().UTC().Format(timeFormat)

	authType := cfg.AuthType
	if authType == "" {
		authType = defaultAuthType
	}

	invokeMode := cfg.InvokeMode
	if invokeMode == "" {
		invokeMode = defaultInvokeMode
	}

	url := &driver.FunctionURLConfig{
		FunctionName: cfg.FunctionName,
		FunctionArn:  fd.info.ARN,
		FunctionURL:  m.functionURL(),
		AuthType:     authType,
		InvokeMode:   invokeMode,
		Cors:         cfg.Cors,
		CreationTime: now,
		LastModified: now,
	}
	fd.urlConfig = url
	m.funcs.Set(cfg.FunctionName, fd)

	result := *url

	return &result, nil
}

// GetFunctionURLConfig returns the Function URL for a function.
func (m *Mock) GetFunctionURLConfig(_ context.Context, functionName string) (*driver.FunctionURLConfig, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if fd.urlConfig == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "function url config for %s not found", functionName)
	}

	result := *fd.urlConfig

	return &result, nil
}

// UpdateFunctionURLConfig mutates AuthType/InvokeMode/Cors on an existing URL.
//
//nolint:gocritic // hugeParam: matches the functionURLManager interface signature.
func (m *Mock) UpdateFunctionURLConfig(_ context.Context, cfg driver.FunctionURLConfig) (*driver.FunctionURLConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if fd.urlConfig == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "function url config for %s not found", cfg.FunctionName)
	}

	updated := *fd.urlConfig

	if cfg.AuthType != "" {
		updated.AuthType = cfg.AuthType
	}

	if cfg.InvokeMode != "" {
		updated.InvokeMode = cfg.InvokeMode
	}

	if cfg.Cors != nil {
		updated.Cors = cfg.Cors
	}

	updated.LastModified = m.opts.Clock.Now().UTC().Format(timeFormat)
	fd.urlConfig = &updated
	m.funcs.Set(cfg.FunctionName, fd)

	result := updated

	return &result, nil
}

// DeleteFunctionURLConfig removes a function's URL.
func (m *Mock) DeleteFunctionURLConfig(_ context.Context, functionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if fd.urlConfig == nil {
		return cerrors.Newf(cerrors.NotFound, "function url config for %s not found", functionName)
	}

	fd.urlConfig = nil
	m.funcs.Set(functionName, fd)

	return nil
}

// ListFunctionURLConfigs returns the Function URL configs for a function (zero
// or one, since a function has at most one URL for the $LATEST qualifier).
func (m *Mock) ListFunctionURLConfigs(_ context.Context, functionName string) ([]driver.FunctionURLConfig, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if fd.urlConfig == nil {
		return []driver.FunctionURLConfig{}, nil
	}

	return []driver.FunctionURLConfig{*fd.urlConfig}, nil
}

// functionURL synthesizes a Lambda Function URL of the real shape
// https://<url-id>.lambda-url.<region>.on.aws/.
func (m *Mock) functionURL() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("https://cloudemu.lambda-url.%s.on.aws/", m.opts.Region)
	}

	return fmt.Sprintf("https://%s.lambda-url.%s.on.aws/", hex.EncodeToString(b[:8])+hex.EncodeToString(b[8:]), m.opts.Region)
}
