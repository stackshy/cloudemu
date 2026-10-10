package s3

import (
	"bytes"
	"encoding/base64"
	"net/http"
)

// md5DigestLen is the length of a raw MD5 digest in bytes.
const md5DigestLen = 16

// checkContentMD5 verifies the Content-MD5 request header against body, as S3
// does for PutObject and UploadPart. It returns true when the header is absent
// or matches. Otherwise it writes the S3 error and returns false: a value that
// is not the base64 of a 16-byte digest is 400 InvalidDigest, and a digest that
// differs from the body's MD5 is 400 BadDigest. Content-MD5 is a transport
// integrity check on the request bytes, so it lives in the wire layer.
func checkContentMD5(w http.ResponseWriter, h http.Header, body []byte) bool {
	sent := h.Get("Content-MD5")
	if sent == "" {
		return true
	}

	want, err := base64.StdEncoding.DecodeString(sent)
	if err != nil || len(want) != md5DigestLen {
		writeError(w, http.StatusBadRequest, "InvalidDigest", "The Content-MD5 you specified was invalid.")
		return false
	}

	if !bytes.Equal(want, md5Sum(body)) {
		writeError(w, http.StatusBadRequest, "BadDigest", "The Content-MD5 you specified did not match what we received.")
		return false
	}

	return true
}
