# Terraform / OpenTofu

CloudEmu speaks the real cloud wire protocols, so **real Terraform and OpenTofu
run against it** — `init`, `apply`, `plan`, `destroy` — with no Terraform
plugins or shims. You point the provider's endpoints at a running CloudEmu and
apply unmodified resources. The HCL is identical on Terraform and OpenTofu.

> This is continuously proven: `contrib/terraform` drives a real `tofu` binary
> through `apply → plan → destroy` against CloudEmu in CI, asserting the
> post-apply plan is empty (no perpetual diff). See
> [What's verified](#whats-verified).

## Fastest path: the `cloudemu-tf` wrapper

`contrib/terraform/cloudemu-tf` is a drop-in wrapper (the CloudEmu equivalent of
LocalStack's `tflocal`). It writes a provider override pointing at CloudEmu and
supplies dummy credentials, then execs the real `tofu`/`terraform`:

```sh
# 1. start CloudEmu (AWS on :4566)
cloudemu serve &

# 2. put the wrapper on your PATH
ln -s "$PWD/contrib/terraform/cloudemu-tf" /usr/local/bin/cloudemu-tf

# 3. run terraform as usual, from a dir with your .tf files
cloudemu-tf init
cloudemu-tf apply
```

Your config needs only an empty provider block — no endpoints, credentials or
skip flags:

```hcl
provider "aws" {}
```

Configure the wrapper with env vars:

| Variable | Default | Meaning |
|----------|---------|---------|
| `CLOUDEMU_ENDPOINT` | `http://localhost:4566` | CloudEmu AWS endpoint |
| `AWS_REGION` | `us-east-1` | region to report |
| `CLOUDEMU_TF_BIN` | `tofu`, then `terraform` | binary to run |

The wrapper leaves a `cloudemu_providers_override.tf` next to your config; it is
regenerated each run and safe to delete or `.gitignore`.

## Manual provider config (AWS)

If you'd rather not use the wrapper, add the endpoints yourself. This is the
same block LocalStack and floci use:

```hcl
provider "aws" {
  access_key = "test"
  secret_key = "test"
  region     = "us-east-1"

  # CloudEmu is path-style and needs no real-AWS preflight.
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    s3       = "http://localhost:4566"
    dynamodb = "http://localhost:4566"
    ec2      = "http://localhost:4566"
    iam      = "http://localhost:4566"
    sts      = "http://localhost:4566"
    # ...one line per additional service you use
  }
}
```

## Azure and GCP

CloudEmu serves Azure on `:4568` (HTTPS) and GCP on `:4569`. The HTTPS ports use
a self-signed cert, so point your client at the CloudEmu CA or disable
verification for local use.

**GCP** — the `google` provider accepts a per-service `*_custom_endpoint`, so
point each service you use at the GCP port:

```hcl
provider "google" {
  project = "cloudemu"
  # e.g. storage_custom_endpoint = "http://localhost:4569/storage/v1/"
}
```

**Azure** — the `azurerm` provider has no per-service endpoint override. Instead
it resolves every endpoint from an Azure *metadata* document and mints a bearer
token from an AAD OAuth2 endpoint. CloudEmu serves both, so an **unmodified**
`azurerm` provider bootstraps against the emulator by pointing
`ARM_METADATA_HOSTNAME` at the Azure port — the metadata document CloudEmu
returns references itself, so Resource Manager and token traffic route straight
back to the emulator.

The `azurerm` provider is a Go binary and verifies TLS with no skip-verify flag,
so it must trust CloudEmu's cert. Run the Azure port with a cert you generate,
then hand the same cert to Terraform via `SSL_CERT_FILE` (honored by Go's TLS
stack on Linux; on macOS run Terraform in a Linux container or add the cert to
the system keychain):

```sh
# 1. a cert whose SANs cover the host Terraform dials
openssl req -x509 -newkey rsa:2048 -nodes -keyout key.pem -out cert.pem -days 2 \
  -subj "/CN=cloudemu" \
  -addext "subjectAltName=IP:127.0.0.1,DNS:localhost" \
  -addext "basicConstraints=critical,CA:TRUE"

# 2. serve Azure with that cert
cloudemu serve -providers=azure -azure-port 4568 \
  -tls-cert cert.pem -tls-key key.pem \
  -azure-subscription 00000000-0000-0000-0000-0000000000ab
```

```sh
# 3. Terraform env — any credentials work (CloudEmu never verifies them)
export ARM_METADATA_HOSTNAME=127.0.0.1:4568
export ARM_SUBSCRIPTION_ID=00000000-0000-0000-0000-0000000000ab
export ARM_TENANT_ID=11111111-1111-1111-1111-111111111111
export ARM_CLIENT_ID=00000000-0000-0000-0000-000000000001
export ARM_CLIENT_SECRET=any
export ARM_RESOURCE_PROVIDER_REGISTRATIONS=none
export SSL_CERT_FILE=$PWD/cert.pem
```

The config needs only an empty provider block:

```hcl
provider "azurerm" {
  features {}
}
```

`init → apply → plan(no-diff) → destroy` then runs unmodified — verified with
`azurerm` v4 against `azurerm_resource_group` and `azurerm_storage_account`.

> AWS is the most exercised surface today, and the only one with an automated
> idempotency suite. The GCP block above works but is not yet suite-covered, and
> the Azure recipe above is proven by hand but not yet suite-covered —
> contributions of fixtures for either are welcome.

## What's verified

The `contrib/terraform` suite asserts `apply → plan(no-diff) → destroy` against a
real Terraform binary. Currently covered:

- **S3** — `aws_s3_bucket`
- **DynamoDB** — `aws_dynamodb_table` (`PAY_PER_REQUEST` and `PROVISIONED`)
- **IAM** — `aws_iam_role`
- **Networking** — `aws_vpc`, `aws_subnet`, `aws_security_group`,
  `aws_route_table`, `aws_route_table_association`
- **The wrapper** — the same flow through `cloudemu-tf` with only an empty
  provider block

`contrib/terraform/README.md` tracks the exact known limits (which sub-resources
are not yet persisted, etc.). To run the suite locally:

```sh
cd contrib/terraform
go test ./...   # needs a tofu or terraform binary on PATH
```
