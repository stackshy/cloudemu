resource "aws_kms_key" "logs" {
  description             = "cloudemu-tf-logs"
  deletion_window_in_days = 7
}

resource "aws_cloudwatch_log_group" "encrypted" {
  name              = "/cloudemu-tf/encrypted"
  retention_in_days = 7
  kms_key_id        = aws_kms_key.logs.arn
  log_group_class   = "STANDARD"

  tags = {
    env = "test"
  }
}

resource "aws_cloudwatch_log_group" "infrequent" {
  name              = "/cloudemu-tf/infrequent"
  retention_in_days = 0
  log_group_class   = "INFREQUENT_ACCESS"
}

resource "aws_cloudwatch_log_stream" "s1" {
  name           = "s1"
  log_group_name = aws_cloudwatch_log_group.encrypted.name
}
