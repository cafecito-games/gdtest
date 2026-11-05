package doubles

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// uidAlphabet is Godot's base-34 digit set, ordered by digit value.
const uidAlphabet = "abcdefghijklmnopqrstuvwxy012345678"

// uidDomain separates the digests of generated double uids from any other
// digest of the same resource path.
const uidDomain = "gdtest-double-uid\x00"

// uidPrefix starts every uid text, as Godot writes it.
const uidPrefix = "uid://"

// UIDFor returns the content of the uid sidecar of the generated double written
// to outputResource, which is a res:// path. The identifier is derived from the
// path alone, so regenerating a double never changes its uid.
func UIDFor(outputResource string) string {
	sum := sha256.Sum256([]byte(uidDomain + outputResource))
	id := binary.LittleEndian.Uint64(sum[:8]) & 0x7FFFFFFFFFFFFFFF
	return uidPrefix + encodeUID(id) + "\n"
}

// encodeUID writes id in base 34, most significant digit first, with at least
// one digit. It matches the encoder of gdkit's uid package.
func encodeUID(id uint64) string {
	base := uint64(len(uidAlphabet))
	digits := make([]byte, 0, 13)
	for {
		digits = append(digits, uidAlphabet[id%base])
		id /= base
		if id == 0 {
			break
		}
	}
	var text strings.Builder
	for index := len(digits) - 1; index >= 0; index-- {
		text.WriteByte(digits[index])
	}
	return text.String()
}
