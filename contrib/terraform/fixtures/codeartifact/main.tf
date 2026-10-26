resource "aws_kms_key" "artifacts" {
  description             = "cloudemu-tf-codeartifact"
  deletion_window_in_days = 7
}

resource "aws_codeartifact_domain" "main" {
  domain         = "cloudemu-tf"
  encryption_key = aws_kms_key.artifacts.arn

  tags = {
    env = "test"
  }
}

# One repository holds the external connection; the other consumes it as an upstream.
resource "aws_codeartifact_repository" "npm_store" {
  repository  = "npm-store"
  domain      = aws_codeartifact_domain.main.domain
  description = "proxy of the public npm registry"

  external_connections {
    external_connection_name = "public:npmjs"
  }
}

resource "aws_codeartifact_repository" "app" {
  repository  = "app"
  domain      = aws_codeartifact_domain.main.domain
  description = "application packages"

  upstream {
    repository_name = aws_codeartifact_repository.npm_store.repository
  }

  tags = {
    team = "platform"
  }
}

resource "aws_codeartifact_domain_permissions_policy" "main" {
  domain = aws_codeartifact_domain.main.domain

  policy_document = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::123456789012:root" }
      Action    = ["codeartifact:CreateRepository", "codeartifact:DescribeDomain", "codeartifact:GetAuthorizationToken"]
      Resource  = "*"
    }]
  })
}

resource "aws_codeartifact_repository_permissions_policy" "app" {
  repository = aws_codeartifact_repository.app.repository
  domain     = aws_codeartifact_domain.main.domain

  policy_document = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::123456789012:root" }
      Action    = ["codeartifact:ReadFromRepository", "codeartifact:DescribePackageVersion"]
      Resource  = "*"
    }]
  })
}
