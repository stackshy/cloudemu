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

# A metric-math alarm on an error rate. The Metrics list must round-trip
# through DescribeAlarms or the post-apply plan shows a diff.
resource "aws_cloudwatch_metric_alarm" "rate" {
  alarm_name          = "error-rate"
  evaluation_periods  = 1
  threshold           = 20
  comparison_operator = "GreaterThanThreshold"

  metric_query {
    id = "err"

    metric {
      namespace   = "M/App"
      metric_name = "Errors"
      period      = 60
      stat        = "Sum"
      dimensions = {
        Service = "api"
      }
    }
  }

  metric_query {
    id = "req"

    metric {
      namespace   = "M/App"
      metric_name = "Requests"
      period      = 60
      stat        = "Sum"
      dimensions = {
        Service = "api"
      }
    }
  }

  metric_query {
    id          = "rate"
    expression  = "err/req*100"
    label       = "ErrorRate"
    return_data = true
  }
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

# An anomaly detection alarm. Its band and threshold_metric_id must round-trip
# through DescribeAlarms or the post-apply plan shows a diff.
resource "aws_cloudwatch_metric_alarm" "latency_band" {
  alarm_name          = "latency-band"
  evaluation_periods  = 1
  comparison_operator = "LessThanLowerOrGreaterThanUpperThreshold"
  threshold_metric_id = "ad1"

  metric_query {
    id          = "m1"
    return_data = true

    metric {
      namespace   = "A/App"
      metric_name = "Lat"
      period      = 60
      stat        = "Average"
    }
  }

  metric_query {
    id          = "ad1"
    expression  = "ANOMALY_DETECTION_BAND(m1, 2)"
    label       = "Lat (expected)"
    return_data = true
  }
}
