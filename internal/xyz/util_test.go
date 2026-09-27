package xyz

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

func itoa[T uint8 | uint32](v T) string { return strconv.FormatUint(uint64(v), 10) }

func fixturesSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
