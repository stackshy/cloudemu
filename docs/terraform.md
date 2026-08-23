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

**Azure** — the `azurerm` provider reads its endpoint from `ARM_*` environment
variables / a custom metadata host rather than an `endpoints` block, so the
cleanest path is to run against the Azure port and skip provider registration:

```hcl
provider "azurerm" {
  features {}
  skip_provider_registration = true
  # metadata_host / resource_manager_endpoint → https://localhost:4568
}
```

**GCP** — the `google` provider accepts a per-service `*_custom_endpoint`:

```hcl
provider "google" {
  project = "cloudemu"
  # e.g. storage_custom_endpoint = "http://localhost:4569/storage/v1/"
}
```

> AWS is the most exercised surface today. Azure/GCP Terraform blocks work but
> are not yet covered by the automated idempotency suite — contributions of new
> fixtures are welcome.

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
