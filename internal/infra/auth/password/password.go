// Package password hashes and verifies local-account passwords: argon2id for
// new hashes, bcrypt accepted for accounts created before the switch.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Argon2id parameters (OWASP recommendation: 19 MiB memory, 2 passes, 1 lane).
// They are stored in each PHC string, so tuning them later only affects new
// hashes; Verify always uses the parameters in the hash.
const (
	memoryKiB uint32 = 19456
	passes    uint32 = 2
	lanes     uint8  = 1
	saltLen          = 16
	keyLen    uint32 = 32
)

// Verification caps: a crafted stored string must not drive unbounded
// allocation.
const (
	maxMemoryKiB uint32 = 4194304 // 4 GiB
	maxPasses    uint32 = 64
	maxLanes     uint8  = 16
)

// Hash returns an argon2id PHC string:
// $argon2id$v=19$m=<KiB>,t=<passes>,p=<lanes>$<b64 salt>$<b64 key> (raw std base64).
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	return encode(pw, salt, memoryKiB, passes, lanes, keyLen), nil
}

func encode(pw string, salt []byte, m, t uint32, p uint8, kl uint32) string {
	key := argon2.IDKey([]byte(pw), salt, t, m, p, kl)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// Verify checks pw against an argon2id PHC or a bcrypt ($2a$/$2b$/$2y$) hash.
// A mismatch or malformed/unknown hash is ok=false with a nil error.
// needsRehash is set only when ok is true and the hash is bcrypt or argon2id
// with weaker parameters than Hash currently produces.
func Verify(hash, pw string) (ok bool, needsRehash bool, err error) {
	switch {
	case strings.HasPrefix(hash, "$argon2id$"):
		m, t, p, good := verifyArgon2id(hash, pw)
		if !good {
			return false, false, nil
		}
		return true, m < memoryKiB || t < passes || p < lanes, nil
	case strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"), strings.HasPrefix(hash, "$2y$"):
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
			return false, false, nil
		}
		return true, true, nil
	default:
		return false, false, nil
	}
}

// verifyArgon2id recomputes the key with the stored parameters and compares in
// constant time. Anything malformed fails closed.
func verifyArgon2id(stored, pw string) (m, t uint32, p uint8, ok bool) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return
	}
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || n != 3 || m == 0 || t == 0 || p == 0 {
		return
	}
	if m > maxMemoryKiB || t > maxPasses || p > maxLanes {
		return
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return
	}
	computed := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(key)))
	ok = subtle.ConstantTimeCompare(computed, key) == 1
	return
}
