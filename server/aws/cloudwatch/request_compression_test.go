package cloudwatch_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

const compressedMetrics = 600

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func postCompressed(t *testing.T, target string, headers map[string]string, body []byte) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body)) //nolint:noctx // test request
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}

func listedMetrics(t *testing.T, ts string, namespace string) int {
	t.Helper()

	n := 0
	token := ""

	for {
		body := fmt.Sprintf(`{"Namespace":%q,"NextToken":%q}`, namespace, token)

		status, raw := postCompressed(t, ts, map[string]string{
			"Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": jsonTarget + "ListMetrics",
		}, []byte(body))
		if status != http.StatusOK {
			t.Fatalf("ListMetrics: %d %s", status, raw)
		}

		n += strings.Count(raw, `"MetricName"`)

		i := strings.Index(raw, `"NextToken":"`)
		if i < 0 {
			return n
		}

		rest := raw[i+len(`"NextToken":"`):]
		token = rest[:strings.Index(rest, `"`)]
	}
}

// TestCompressedPutMetricData covers the gzip request body the SDKs send for
// a large PutMetricData, on all three protocols.
func TestCompressedPutMetricData(t *testing.T) {
	ts := newJSONServer(t)

	t.Run("json", func(t *testing.T) {
		var data []string
		for i := range compressedMetrics {
			data = append(data, fmt.Sprintf(`{"MetricName":"J%04d","Value":1}`, i))
		}

		body := gzipBytes(t, []byte(`{"Namespace":"GzJSON","MetricData":[`+strings.Join(data, ",")+`]}`))

		status, raw := postCompressed(t, ts.URL, map[string]string{
			"Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": jsonTarget + "PutMetricData",
			"Content-Encoding": "gzip",
		}, body)
		if status != http.StatusOK {
			t.Fatalf("PutMetricData: %d %s", status, raw)
		}

		if n := listedMetrics(t, ts.URL, "GzJSON"); n != compressedMetrics {
			t.Fatalf("listed %d metrics, want %d", n, compressedMetrics)
		}
	})

	t.Run("query", func(t *testing.T) {
		form := url.Values{"Action": {"PutMetricData"}, "Version": {"2010-08-01"}, "Namespace": {"GzQuery"}}
		for i := range compressedMetrics {
			p := "MetricData.member." + strconv.Itoa(i+1) + "."
			form.Set(p+"MetricName", fmt.Sprintf("Q%04d", i))
			form.Set(p+"Value", "1")
		}

		status, raw := postCompressed(t, ts.URL, map[string]string{
			"Content-Type": "application/x-www-form-urlencoded", "Authorization": monitoringAuth,
			"Content-Encoding": "gzip",
		}, gzipBytes(t, []byte(form.Encode())))
		if status != http.StatusOK {
			t.Fatalf("PutMetricData: %d %s", status, raw)
		}

		if n := listedMetrics(t, ts.URL, "GzQuery"); n != compressedMetrics {
			t.Fatalf("listed %d metrics, want %d", n, compressedMetrics)
		}
	})

	t.Run("cbor sdk", func(t *testing.T) {
		client, ctx := newCWClient(t)

		data := make([]cwtypes.MetricDatum, 0, compressedMetrics)
		for i := range compressedMetrics {
			data = append(data, cwtypes.MetricDatum{MetricName: aws.String(fmt.Sprintf("C%04d", i)), Value: aws.Float64(1)})
		}

		if _, err := client.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("GzCBOR"), MetricData: data,
		}); err != nil {
			t.Fatalf("PutMetricData: %v", err)
		}

		n := 0

		p := awscw.NewListMetricsPaginator(client, &awscw.ListMetricsInput{Namespace: aws.String("GzCBOR")})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				t.Fatalf("ListMetrics: %v", err)
			}

			n += len(page.Metrics)
		}

		if n != compressedMetrics {
			t.Fatalf("listed %d metrics, want %d", n, compressedMetrics)
		}
	})

	t.Run("bad gzip", func(t *testing.T) {
		status, raw := postCompressed(t, ts.URL, map[string]string{
			"Content-Type": "application/x-amz-json-1.0", "X-Amz-Target": jsonTarget + "PutMetricData",
			"Content-Encoding": "gzip",
		}, []byte("not gzip"))
		if status != http.StatusBadRequest || !strings.Contains(raw, "SerializationException") {
			t.Fatalf("bad gzip: %d %s", status, raw)
		}
	})
}
