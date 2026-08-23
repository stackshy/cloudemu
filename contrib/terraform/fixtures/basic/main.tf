resource "aws_s3_bucket" "b" {
  bucket = "cloudemu-tf-basic"
}

resource "aws_dynamodb_table" "t" {
  name         = "cloudemu-tf-basic"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_iam_role" "r" {
  name = "cloudemu-tf-basic"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}
