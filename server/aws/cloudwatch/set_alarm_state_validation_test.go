package cloudwatch_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/stackshy/cloudemu/v2/config"
	cwprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	cwserver "github.com/stackshy/cloudemu/v2/server/aws/cloudwatch"
)

// TestSetAlarmStateValidation checks the SetAlarmState rules from the API
// reference on both protocols. A rejected call must leave the state as it was.
func TestSetAlarmStateValidation(t *testing.T) {
	tests := []struct {
		name       string
		in         awscw.SetAlarmStateInput
		wantStatus int
		wantCode   string
		wantState  string
	}{
		{
			name:       "bad state value",
			in:         awscw.SetAlarmStateInput{StateValue: "BOGUS", StateReason: aws.String("x")},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError", wantState: "ALARM",
		},
		{
			name:       "reason too long",
			in:         awscw.SetAlarmStateInput{StateValue: cwtypes.StateValueOk, StateReason: aws.String(strings.Repeat("r", 1024))},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError", wantState: "ALARM",
		},
		{
			name:       "reason at limit counts characters",
			in:         awscw.SetAlarmStateInput{StateValue: cwtypes.StateValueOk, StateReason: aws.String(strings.Repeat("é", 1023))},
			wantStatus: http.StatusOK, wantState: "OK",
		},
		{
			name: "reason data not json",
			in: awscw.SetAlarmStateInput{
				StateValue: cwtypes.StateValueOk, StateReason: aws.String("x"), StateReasonData: aws.String("{not json"),
			},
			wantStatus: http.StatusBadRequest, wantCode: "InvalidFormat", wantState: "ALARM",
		},
		{
			name: "reason data too long",
			in: awscw.SetAlarmStateInput{
				StateValue: cwtypes.StateValueOk, StateReason: aws.String("x"),
				StateReasonData: aws.String(`"` + strings.Repeat("d", 3999) + `"`),
			},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError", wantState: "ALARM",
		},
		{
			name: "valid reason data",
			in: awscw.SetAlarmStateInput{
				StateValue: cwtypes.StateValueOk, StateReason: aws.String("x"), StateReasonData: aws.String(`{"k":1}`),
			},
			wantStatus: http.StatusOK, wantState: "OK",
		},
		{
			name:       "unknown alarm",
			in:         awscw.SetAlarmStateInput{AlarmName: aws.String("nope"), StateValue: cwtypes.StateValueOk, StateReason: aws.String("x")},
			wantStatus: http.StatusNotFound, wantCode: "ResourceNotFound", wantState: "ALARM",
		},
	}

	for _, p := range cwProtocols() {
		for _, tc := range tests {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				w := p.build(t, nil)
				w.putAlarm(t, &awscw.PutMetricAlarmInput{
					AlarmName: aws.String("a1"), Namespace: aws.String("ns"), MetricName: aws.String("m"),
					ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold, EvaluationPeriods: aws.Int32(1),
					Period: aws.Int32(60), Threshold: aws.Float64(1), Statistic: cwtypes.StatisticSum,
				})

				if status, code := w.setAlarmState(t, &awscw.SetAlarmStateInput{
					AlarmName: aws.String("a1"), StateValue: cwtypes.StateValueAlarm, StateReason: aws.String("seed"),
				}); status != http.StatusOK {
					t.Fatalf("seed SetAlarmState: status %d code %s", status, code)
				}

				in := tc.in
				if in.AlarmName == nil {
					in.AlarmName = aws.String("a1")
				}

				status, code := w.setAlarmState(t, &in)
				if status != tc.wantStatus || code != tc.wantCode {
					t.Fatalf("got status %d code %q, want %d %q", status, code, tc.wantStatus, tc.wantCode)
				}

				alarms, err := w.provider.DescribeAlarms(context.Background(), []string{"a1"})
				if err != nil || len(alarms) != 1 {
					t.Fatalf("DescribeAlarms: %v %+v", err, alarms)
				}

				if alarms[0].State != tc.wantState {
					t.Fatalf("state = %q, want %q", alarms[0].State, tc.wantState)
				}
			})
		}
	}
}

// TestSetAlarmStateReasonData: StateReasonData is stored, returned by
// DescribeAlarms and carried into the history on both protocols. A later call
// without it clears it.
func TestSetAlarmStateReasonData(t *testing.T) {
	const data = `{"k":1}`

	for _, p := range cwProtocols() {
		t.Run(p.name, func(t *testing.T) {
			w := p.build(t, nil)
			w.putAlarm(t, &awscw.PutMetricAlarmInput{
				AlarmName: aws.String("a1"), Namespace: aws.String("ns"), MetricName: aws.String("m"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold, EvaluationPeriods: aws.Int32(1),
				Period: aws.Int32(60), Threshold: aws.Float64(1), Statistic: cwtypes.StatisticSum,
			})

			if status, code := w.setAlarmState(t, &awscw.SetAlarmStateInput{
				AlarmName: aws.String("a1"), StateValue: cwtypes.StateValueAlarm,
				StateReason: aws.String("test"), StateReasonData: aws.String(data),
			}); status != http.StatusOK {
				t.Fatalf("SetAlarmState: status %d code %s", status, code)
			}

			if got := w.reasonData(t, "a1"); got != data {
				t.Fatalf("StateReasonData = %q, want %q", got, data)
			}

			history := w.historyData(t, "a1")
			if len(history) == 0 || !strings.Contains(history[0], `"newState":{"stateValue":"ALARM","stateReasonData":{"k":1}}`) {
				t.Fatalf("HistoryData = %q", history)
			}

			if status, code := w.setAlarmState(t, &awscw.SetAlarmStateInput{
				AlarmName: aws.String("a1"), StateValue: cwtypes.StateValueOk, StateReason: aws.String("clear"),
			}); status != http.StatusOK {
				t.Fatalf("SetAlarmState: status %d code %s", status, code)
			}

			if got := w.reasonData(t, "a1"); got != "" {
				t.Fatalf("StateReasonData after a call without it = %q, want empty", got)
			}
		})
	}
}

// TestQuerySetAlarmStateMissingReason: StateReason is required. The SDK checks
// this on the client, so only a raw query request can leave it out.
func TestQuerySetAlarmStateMissingReason(t *testing.T) {
	ts := httptest.NewServer(cwserver.New(cwprovider.New(config.NewOptions())))
	t.Cleanup(ts.Close)

	form := url.Values{"Action": {"SetAlarmState"}, "AlarmName": {"a1"}, "StateValue": {"OK"}}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", monitoringAuth)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "<Code>ValidationError</Code>") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}
