package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// pbkdf2SHA256 mirrors hashlib.pbkdf2_hmac("sha256", ...) used by werkzeug.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		U = prf.Sum(U[:0])

		T := make([]byte, len(U))
		copy(T, U)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(U)
			U = prf.Sum(U[:0])
			for i := range T {
				T[i] ^= U[i]
			}
		}
		dk = append(dk, T...)
	}
	return dk[:keyLen]
}

var _ hash.Hash = sha256.New()
