# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| latest  | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in CloudEmu, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please email **nitinraj7488204975@gmail.com** with:

- A description of the vulnerability
- Steps to reproduce the issue
- Any potential impact

We will acknowledge receipt within 48 hours and aim to provide a fix within 7 days for critical issues.

## Scope

CloudEmu is an in-memory testing library and does not handle production traffic, secrets, or real cloud credentials. However, we still take security seriously in the following areas:

- **Code injection** via user-provided inputs (policy documents, filter patterns)
- **Denial of service** via unbounded memory allocation
- **Dependency vulnerabilities** in Go modules

## Best Practices for Users

- Never use CloudEmu in production environments — it is designed for testing only
- Do not commit real cloud credentials in test files
- Keep your Go dependencies up to date with `go get -u ./...`
