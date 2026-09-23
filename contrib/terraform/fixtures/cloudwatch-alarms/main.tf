# CloudWatch metric alarms that set a Unit. The unit must round-trip through
# DescribeAlarms or the post-apply plan shows a diff.

resource "aws_cloudwatch_metric_alarm" "cpu_percent" {
  alarm_name          = "cpu-percent"
  namespace           = "AWS/EC2"
  metric_name         = "CPUUtilization"
  statistic           = "Average"
  unit                = "Percent"
  period              = 60
  evaluation_periods  = 1
  threshold           = 80
  comparison_operator = "GreaterThanThreshold"
}

# Data for U/App Cpu is put as Percent, so this alarm never sees it and stays
# in INSUFFICIENT_DATA.
resource "aws_cloudwatch_metric_alarm" "wrong_unit" {
  alarm_name          = "wrong-unit"
  namespace           = "U/App"
  metric_name         = "Cpu"
  statistic           = "Average"
  unit                = "Bytes"
  period              = 60
  evaluation_periods  = 1
  threshold           = 50
  comparison_operator = "GreaterThanThreshold"
}
