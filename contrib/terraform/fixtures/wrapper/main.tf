terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# No endpoints, credentials or skip flags here — cloudemu-tf injects them via a
# generated override. This is the whole point: unmodified-looking config runs
# against CloudEmu.
provider "aws" {}

resource "aws_s3_bucket" "b" {
  bucket = "cloudemu-tf-wrapper"
}

resource "aws_dynamodb_table" "t" {
  name         = "cloudemu-tf-wrapper"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }
}
