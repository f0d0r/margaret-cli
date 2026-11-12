// Package headprint stores a compact membership fingerprint of a book's
// leading text. Matching uses it to check whether an OpenLibrary title or
// author appears in the scanned content, which confirms ISBN matches whose
// scanned metadata is wrong.
package headprint

import (
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

const (
	// BloomBytes is the size of the fingerprint stored per book file.
	BloomBytes = 8 << 10
	bloomBits  = BloomBytes * 8
	hashCount  = 4

	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// Build returns a Bloom filter over the distinctive tokens of head. It always
// returns a full-sized filter so a recorded file is not parsed again merely
// because its head was empty.
func Build(head string) []byte {
	tokens := textmatch.ContentTokens(head)
	bloom := make([]byte, BloomBytes)
	for _, token := range tokens {
		add(bloom, token)
	}
	return bloom
}

// Contains reports whether token may be present in the fingerprint.
func Contains(bloom []byte, token string) bool {
	if len(bloom) != BloomBytes {
		return false
	}
	h1, h2 := hashToken(token)
	for i := uint32(0); i < hashCount; i++ {
		position := (h1 + i*h2) % bloomBits
		if bloom[position/8]&(1<<(position%8)) == 0 {
			return false
		}
	}
	return true
}

// Containment returns the fraction of tokens present in the fingerprint. It
// returns 0 when tokens is empty.
func Containment(bloom []byte, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	found := 0
	for _, token := range tokens {
		if Contains(bloom, token) {
			found++
		}
	}
	return float64(found) / float64(len(tokens))
}

func add(bloom []byte, token string) {
	h1, h2 := hashToken(token)
	for i := uint32(0); i < hashCount; i++ {
		position := (h1 + i*h2) % bloomBits
		bloom[position/8] |= 1 << (position % 8)
	}
}

func hashToken(token string) (uint32, uint32) {
	sum := uint64(fnvOffset64)
	for i := 0; i < len(token); i++ {
		sum ^= uint64(token[i])
		sum *= fnvPrime64
	}
	second := uint32(sum>>32) | 1
	return uint32(sum), second
}
