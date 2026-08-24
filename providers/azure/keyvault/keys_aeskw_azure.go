package keyvault

import (
	"crypto/aes"
	"crypto/subtle"
	"encoding/binary"

	"github.com/stackshy/cloudemu/v2/errors"
)

const (
	// aesKWBlock is the 64-bit half-block size used by RFC 3394 AES key wrap.
	aesKWBlock = 8
	// aesKWRounds is the fixed number of wrapping rounds in RFC 3394.
	aesKWRounds = 6

	aes128KeyLen = 16
	aes192KeyLen = 24
	aes256KeyLen = 32

	// minWrapBlocks is the smallest plaintext (in 64-bit blocks) RFC 3394 wraps.
	minWrapBlocks = 2
)

// defaultIV is the RFC 3394 initial value used to detect a valid unwrap.
var defaultIV = []byte{0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6} //nolint:gochecknoglobals // fixed RFC 3394 constant

func aesKWKeyLen(alg string) (int, bool) {
	switch alg {
	case algA128KW:
		return aes128KeyLen, true
	case algA192KW:
		return aes192KeyLen, true
	case algA256KW:
		return aes256KeyLen, true
	default:
		return 0, false
	}
}

// aesKeyWrap implements the RFC 3394 AES Key Wrap algorithm.
func aesKeyWrap(kek []byte, alg string, plaintext []byte) ([]byte, error) {
	if want, ok := aesKWKeyLen(alg); !ok || len(kek) != want {
		return nil, errors.Newf(errors.InvalidArgument, "algorithm %q does not match key length", alg)
	}

	if len(plaintext) < minWrapBlocks*aesKWBlock || len(plaintext)%aesKWBlock != 0 {
		return nil, errors.New(errors.InvalidArgument, "AES key wrap input must be a multiple of 8 bytes and at least 16 bytes")
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "aes cipher: %v", err)
	}

	n := len(plaintext) / aesKWBlock
	r := make([]byte, len(plaintext))
	copy(r, plaintext)

	a := make([]byte, aesKWBlock)
	copy(a, defaultIV)

	buf := make([]byte, minWrapBlocks*aesKWBlock)

	for j := 0; j < aesKWRounds; j++ {
		for i := 1; i <= n; i++ {
			copy(buf[:aesKWBlock], a)
			copy(buf[aesKWBlock:], r[(i-1)*aesKWBlock:i*aesKWBlock])
			block.Encrypt(buf, buf)

			copy(a, buf[:aesKWBlock])
			binary.BigEndian.PutUint64(a, binary.BigEndian.Uint64(a)^uint64(n*j+i))
			copy(r[(i-1)*aesKWBlock:i*aesKWBlock], buf[aesKWBlock:])
		}
	}

	return append(append([]byte(nil), a...), r...), nil
}

// aesKeyUnwrap reverses aesKeyWrap and verifies the RFC 3394 integrity check.
func aesKeyUnwrap(kek []byte, alg string, ciphertext []byte) ([]byte, error) {
	if want, ok := aesKWKeyLen(alg); !ok || len(kek) != want {
		return nil, errors.Newf(errors.InvalidArgument, "algorithm %q does not match key length", alg)
	}

	if len(ciphertext) < (minWrapBlocks+1)*aesKWBlock || len(ciphertext)%aesKWBlock != 0 {
		return nil, errors.New(errors.InvalidArgument, "AES key unwrap input is malformed")
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "aes cipher: %v", err)
	}

	n := len(ciphertext)/aesKWBlock - 1
	a := make([]byte, aesKWBlock)
	copy(a, ciphertext[:aesKWBlock])

	r := make([]byte, n*aesKWBlock)
	copy(r, ciphertext[aesKWBlock:])

	buf := make([]byte, minWrapBlocks*aesKWBlock)

	for j := aesKWRounds - 1; j >= 0; j-- {
		for i := n; i >= 1; i-- {
			binary.BigEndian.PutUint64(a, binary.BigEndian.Uint64(a)^uint64(n*j+i)) //nolint:gosec // n, j, i are small non-negative loop counters
			copy(buf[:aesKWBlock], a)
			copy(buf[aesKWBlock:], r[(i-1)*aesKWBlock:i*aesKWBlock])
			block.Decrypt(buf, buf)

			copy(a, buf[:aesKWBlock])
			copy(r[(i-1)*aesKWBlock:i*aesKWBlock], buf[aesKWBlock:])
		}
	}

	if subtle.ConstantTimeCompare(a, defaultIV) != 1 {
		return nil, errors.New(errors.InvalidArgument, "AES key unwrap integrity check failed")
	}

	return r, nil
}
