# cloudemu terraform compatibility

Evidence that real **Terraform / OpenTofu** works against CloudEmu. This module
(its own Go module, so `terraform-exec` stays out of the core) drives the real
`tofu`/`terraform` binary through `init → apply → plan → destroy` against an
**in-process** CloudEmu AWS wire server — no Docker, no subprocess.

The load-bearing assertion is the **post-apply plan**: it must report **no
changes**. A change means a resource's read did not round-trip what `apply`
wrote (a "perpetual diff") — the class of wire-fidelity bug this suite exists to
catch. Each `fixtures/<case>/` is one scenario.

## Point your own Terraform at CloudEmu

Run the standalone server (`cloudemu serve`, AWS on `:4566`) and use this
provider block — the same flags LocalStack and floci use:

```hcl
provider "aws" {
  access_key = "test"
  secret_key = "test"
  region     = "us-east-1"

  s3_use_path_style           = true   # CloudEmu S3 is path-style
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    s3       = "http://localhost:4566"
    dynamodb = "http://localhost:4566"
    iam      = "http://localhost:4566"
    sts      = "http://localhost:4566"
    # ... one line per service you use
  }
}
```

## Run the suite

```bash
cd contrib/terraform
go test ./...        # needs a `tofu` (preferred) or `terraform` binary on PATH
```

The test prefers OpenTofu (MPL-2.0) and falls back to Terraform; the HCL is
identical on both. It skips cleanly when neither binary is installed.
