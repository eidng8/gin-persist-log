package internal

import (
	"github.com/cespare/xxhash/v2"
)

type Hasher interface {
	New()
	Reset()
	WriteString(string) (int, error)
	Hash() uint64
}

type XxHasher struct {
	hasher *xxhash.Digest
}

func (h *XxHasher) New() {
	h.hasher = xxhash.New()
}

func (h *XxHasher) Reset() {
	h.hasher.Reset()
}

func (h *XxHasher) WriteString(s string) (int, error) {
	return h.hasher.WriteString(s)
}

func (h *XxHasher) Hash() uint64 {
	return h.hasher.Sum64()
}
