terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

variable "endpoint" {
  type        = string
  description = "CloudEmu AWS endpoint URL"
}

provider "aws" {
  access_key = "test"
  secret_key = "test"
  region     = "us-east-1"

  # Point every service at CloudEmu and suppress the real-AWS preflight.
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    ec2 = var.endpoint
    sts = var.endpoint
  }
}
