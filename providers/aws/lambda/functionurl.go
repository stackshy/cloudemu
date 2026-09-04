package lambda

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Function URL defaults applied when the request omits them, matching real
// Lambda.
const (
	defaultInvokeMode = "BUFFERED"
	defaultAuthType   = "NONE"
)

// authTypeAWSIAM is the other AuthType a Function URL config accepts besides
// defaultAuthType ("NONE"). The wire layer accepts any credentials regardless
// of AuthType — SigV4/bearer tokens are parsed but never verified, matching
// the rest of cloudemu — so AWS_IAM is stored and reported faithfully but adds
// no additional enforcement on invoke.
const authTypeAWSIAM = "AWS_IAM"

// invokeModeResponseStream is the other InvokeMode a Function URL config
// accepts besides defaultInvokeMode ("BUFFERED"). The emulator has no chunked/
// streaming execution engine, so RESPONSE_STREAM is stored and reported
// faithfully but invoked the same as BUFFERED.
const invokeModeResponseStream = "RESPONSE_STREAM"

// CreateFunctionURLConfig provisions a Function URL for a function, scoped to
// $LATEST or an alias qualifier (a numbered version is rejected — see
// isVersionQualifier). Only one URL config may exist per (function, qualifier).
//
//nolint:gocritic // hugeParam: matches the functionURLManager interface signature.
func (m *Mock) CreateFunctionURLConfig(_ context.Context, cfg driver.FunctionURLConfig) (*driver.FunctionURLConfig, error) {
	if err := validateAuthType(cfg.AuthType); err != nil {
		return nil, err
	}

	if err := validateInvokeMode(cfg.InvokeMode); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if err := validateFunctionURLQualifier(&fd, cfg.Qualifier); err != nil {
		return nil, err
	}

	key := policyKey(cfg.Qualifier)
	if _, exists := fd.urlConfigs[key]; exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"function url config for %s (qualifier %s) already exists", cfg.FunctionName, key)
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

	stored := &driver.FunctionURLConfig{
		FunctionName: cfg.FunctionName,
		Qualifier:    cfg.Qualifier,
		FunctionArn:  qualifiedARN(fd.info.ARN, cfg.Qualifier),
		FunctionURL:  functionURL(arnRegion(fd.info.ARN, m.opts.Region)),
		AuthType:     authType,
		InvokeMode:   invokeMode,
		Cors:         cloneFunctionURLCors(cfg.Cors),
		CreationTime: now,
		LastModified: now,
	}

	setFunctionURLConfig(&fd, key, stored)
	m.funcs.Set(cfg.FunctionName, fd)

	return cloneFunctionURLConfig(stored), nil
}

// GetFunctionURLConfig returns the Function URL config for a function's
// qualifier ("" and "$LATEST" both mean the unqualified $LATEST URL).
func (m *Mock) GetFunctionURLConfig(_ context.Context, functionName, qualifier string) (*driver.FunctionURLConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	cfg, ok := fd.urlConfigs[policyKey(qualifier)]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function url config for %s not found", functionName)
	}

	return cloneFunctionURLConfig(cfg), nil
}

// UpdateFunctionURLConfig mutates AuthType/InvokeMode/Cors on an existing URL
// config, leaving unset fields unchanged.
//
//nolint:gocritic // hugeParam: matches the functionURLManager interface signature.
func (m *Mock) UpdateFunctionURLConfig(_ context.Context, cfg driver.FunctionURLConfig) (*driver.FunctionURLConfig, error) {
	if err := validateAuthType(cfg.AuthType); err != nil {
		return nil, err
	}

	if err := validateInvokeMode(cfg.InvokeMode); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	key := policyKey(cfg.Qualifier)

	existing, ok := fd.urlConfigs[key]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function url config for %s not found", cfg.FunctionName)
	}

	updated := *existing

	if cfg.AuthType != "" {
		updated.AuthType = cfg.AuthType
	}

	if cfg.InvokeMode != "" {
		updated.InvokeMode = cfg.InvokeMode
	}

	if cfg.Cors != nil {
		updated.Cors = cloneFunctionURLCors(cfg.Cors)
	}

	updated.LastModified = m.opts.Clock.Now().UTC().Format(timeFormat)

	setFunctionURLConfig(&fd, key, &updated)
	m.funcs.Set(cfg.FunctionName, fd)

	return cloneFunctionURLConfig(&updated), nil
}

// DeleteFunctionURLConfig removes a function's URL config for a qualifier.
func (m *Mock) DeleteFunctionURLConfig(_ context.Context, functionName, qualifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	key := policyKey(qualifier)
	if _, exists := fd.urlConfigs[key]; !exists {
		return cerrors.Newf(cerrors.NotFound, "function url config for %s not found", functionName)
	}

	next := make(map[string]*driver.FunctionURLConfig, len(fd.urlConfigs))

	for k, v := range fd.urlConfigs {
		if k != key {
			next[k] = v
		}
	}

	fd.urlConfigs = next
	m.funcs.Set(functionName, fd)

	return nil
}

// ListFunctionURLConfigs returns every Function URL config set on a function
// (one per qualifier).
func (m *Mock) ListFunctionURLConfigs(_ context.Context, functionName string) ([]driver.FunctionURLConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	out := make([]driver.FunctionURLConfig, 0, len(fd.urlConfigs))
	for _, cfg := range fd.urlConfigs {
		out = append(out, *cloneFunctionURLConfig(cfg))
	}

	return out, nil
}

// ResolveFunctionURL finds the Function URL config whose generated FunctionURL
// host matches host (case-insensitive, no port), for the invoke-via-URL wire
// path: the server extracts the Host header from an inbound request and hands
// it here to identify which function/qualifier that URL was assigned to.
func (m *Mock) ResolveFunctionURL(_ context.Context, host string) (*driver.FunctionURLConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	host = strings.ToLower(host)

	for _, name := range m.funcs.Keys() {
		fd, ok := m.funcs.Get(name)
		if !ok {
			continue
		}

		for _, cfg := range fd.urlConfigs {
			if functionURLHost(cfg.FunctionURL) == host {
				return cloneFunctionURLConfig(cfg), nil
			}
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "no function url found for host %s", host)
}

// functionURLHost extracts the lowercase host from a generated FunctionURL
// (https://<url-id>.lambda-url.<region>.on.aws/), or "" if u isn't a valid URL.
func functionURLHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}

	return strings.ToLower(parsed.Hostname())
}

// functionURL synthesizes a Lambda Function URL of the real shape
// https://<url-id>.lambda-url.<region>.on.aws/.
func functionURL(region string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("https://cloudemu.lambda-url.%s.on.aws/", region)
	}

	return fmt.Sprintf("https://%s.lambda-url.%s.on.aws/", hex.EncodeToString(b[:8])+hex.EncodeToString(b[8:]), region)
}

// isVersionQualifier reports whether qualifier names a published numeric
// version ("1", "2", ...) rather than $LATEST or an alias. Real Lambda rejects
// a Function URL config targeting a specific version: the URL must point at
// $LATEST (the function's live code) or an alias, since a numbered version is
// immutable and repointing traffic (e.g. blue/green) only makes sense through
// an alias.
func isVersionQualifier(qualifier string) bool {
	if qualifier == "" || qualifier == latestVersion {
		return false
	}

	_, err := strconv.Atoi(qualifier)

	return err == nil
}

// validateFunctionURLQualifier enforces the qualifiers a Function URL config
// may target: unqualified/$LATEST, or an alias that exists on fd. A numbered
// version, or an alias name that doesn't exist, is rejected.
func validateFunctionURLQualifier(fd *funcData, qualifier string) error {
	if isVersionQualifier(qualifier) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"FunctionUrlConfig cannot target a numbered version (%s); use $LATEST or an alias", qualifier)
	}

	if qualifier == "" || qualifier == latestVersion {
		return nil
	}

	if _, ok := fd.aliases.Get(qualifier); !ok {
		return cerrors.Newf(cerrors.NotFound, "function alias %s not found", qualifier)
	}

	return nil
}

// validateAuthType rejects an AuthType other than the two real Lambda accepts.
func validateAuthType(authType string) error {
	if authType == "" || authType == defaultAuthType || authType == authTypeAWSIAM {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument, "AuthType must be %s or %s, got %s",
		defaultAuthType, authTypeAWSIAM, authType)
}

// validateInvokeMode rejects an InvokeMode other than the two real Lambda
// accepts.
func validateInvokeMode(invokeMode string) error {
	if invokeMode == "" || invokeMode == defaultInvokeMode || invokeMode == invokeModeResponseStream {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument, "InvokeMode must be %s or %s, got %s",
		defaultInvokeMode, invokeModeResponseStream, invokeMode)
}

// setFunctionURLConfig stores cfg under its normalized qualifier key using
// copy-on-write: a fresh map is built so a concurrent reader holding an
// earlier funcData copy still sees an immutable snapshot (-race clean).
func setFunctionURLConfig(fd *funcData, key string, cfg *driver.FunctionURLConfig) {
	next := make(map[string]*driver.FunctionURLConfig, len(fd.urlConfigs)+1)

	for k, v := range fd.urlConfigs {
		next[k] = v
	}

	next[key] = cfg
	fd.urlConfigs = next
}

// cloneFunctionURLCors deep-copies a CORS config so stored state and a
// returned/snapshot copy never share the pointer or its slice fields.
func cloneFunctionURLCors(c *driver.FunctionURLCors) *driver.FunctionURLCors {
	if c == nil {
		return nil
	}

	clone := *c
	clone.AllowHeaders = append([]string(nil), c.AllowHeaders...)
	clone.AllowMethods = append([]string(nil), c.AllowMethods...)
	clone.AllowOrigins = append([]string(nil), c.AllowOrigins...)
	clone.ExposeHeaders = append([]string(nil), c.ExposeHeaders...)

	return &clone
}

// cloneFunctionURLConfig deep-copies cfg so stored state and a returned/
// snapshot copy never share the Cors pointer.
func cloneFunctionURLConfig(cfg *driver.FunctionURLConfig) *driver.FunctionURLConfig {
	clone := *cfg
	clone.Cors = cloneFunctionURLCors(cfg.Cors)

	return &clone
}
