resource "aws_sns_topic" "t" {
  name = "cloudemu-tf-core"

  tags = {
    env = "test"
  }
}

resource "aws_secretsmanager_secret" "s" {
  name = "cloudemu-tf-core"
}

resource "aws_kms_key" "k" {
  description = "cloudemu-tf-core"
}

resource "aws_ssm_parameter" "p" {
  name  = "/cloudemu/tf/core"
  type  = "String"
  value = "hello"
}
