package sigv4

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// maxPresignExpires is the longest X-Amz-Expires SigV4 accepts: seven days.
	maxPresignExpires  = 7 * 24 * time.Hour
	msgExpiresPositive = "X-Amz-Expires must be greater than 0"
	codeAccessDenied   = "AccessDenied"
	// s3Service is the S3 signing service name.
	s3Service = "s3"
)

// presignExpires validates the X-Amz-Expires of a presigned URL and returns
// it. S3 requires the parameter, as a whole number of seconds from 1 to
// 604800; anything else is AuthorizationQueryParametersError (400), so a
// presigned URL can never be valid forever.
func presignExpires(raw string, present bool) (time.Duration, *AuthError) {
	if !present {
		return 0, queryParamsErr("Query-string authentication version 4 requires the X-Amz-Algorithm, X-Amz-Credential, " +
			"X-Amz-Signature, X-Amz-Date, X-Amz-SignedHeaders, and X-Amz-Expires parameters.")
	}

	digits := strings.TrimPrefix(raw, "-")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, queryParamsErr("X-Amz-Expires should be a number")
	}

	if digits != raw {
		return 0, queryParamsErr(msgExpiresPositive)
	}

	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || secs > int64(maxPresignExpires/time.Second) {
		return 0, queryParamsErr("X-Amz-Expires must be less than a week (in seconds) that is 604800")
	}

	if secs < 1 {
		return 0, queryParamsErr(msgExpiresPositive)
	}

	return time.Duration(secs) * time.Second, nil
}

func queryParamsErr(msg string) *AuthError {
	return &AuthError{Code: "AuthorizationQueryParametersError", Message: msg, HTTPStatus: http.StatusBadRequest}
}

// checkSignedAmzHeaders rejects an S3 request that carries an x-amz-* header
// the signature does not cover, as S3 does (403 AccessDenied, "There were
// headers present in the request which were not signed"). Without it a
// captured presigned URL or signed request could be replayed with headers
// that change what the request does, such as x-amz-copy-source,
// x-amz-bypass-governance-retention or x-amz-tagging, and still verify.
// x-amz-content-sha256 is exempt: S3 accepts it unsigned on a presigned URL,
// and its value is bound to the body by checkContentSHA256 anyway. Other
// services do not enforce this, so it applies to the s3 scope only.
func checkSignedAmzHeaders(r *http.Request, in *signInputs) *AuthError {
	if in.service != s3Service {
		return nil
	}

	signed := make(map[string]bool, len(in.signedHeaders))
	for _, h := range in.signedHeaders {
		signed[h] = true
	}

	for name := range r.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") || lower == "x-amz-content-sha256" || signed[lower] {
			continue
		}

		return &AuthError{
			Code:       codeAccessDenied,
			Message:    "There were headers present in the request which were not signed",
			HTTPStatus: unsignedStatus,
		}
	}

	return nil
}
