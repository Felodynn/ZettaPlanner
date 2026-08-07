package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing is argon2id with the parameters encoded into the stored
// string, PHC-style:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
//
// Encoding the parameters rather than assuming them is what makes raising the
// cost later a change to newHash alone: every existing hash keeps verifying
// against the parameters it was made with, and nothing has to be migrated in a
// single window.
type argonParams struct {
	memory  uint32 // KiB
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

// defaultParams follows the OWASP argon2id guidance: 64 MiB, three passes.
var defaultParams = argonParams{
	memory:  64 * 1024,
	time:    3,
	threads: defaultThreads(),
	saltLen: 16,
	keyLen:  32,
}

func defaultThreads() uint8 {
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return uint8(n)
}

// ErrPasswordTooShort is returned before hashing, so the message is about the
// password rather than about a constraint deeper in.
var ErrPasswordTooShort = errors.New("password is shorter than 12 characters")

// MinPasswordLength is the floor. Length, not composition rules: character-class
// requirements push people toward P@ssw0rd1 and buy nothing.
const MinPasswordLength = 12

// HashPassword returns a PHC-encoded argon2id hash.
func HashPassword(plain string) (string, error) {
	if len([]rune(plain)) < MinPasswordLength {
		return "", fmt.Errorf("%w; use a longer passphrase rather than a more punctuated short one", ErrPasswordTooShort)
	}
	return hashWith(plain, defaultParams)
}

func hashWith(plain string, p argonParams) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating a password salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, p.time, p.memory, p.threads, p.keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether plain matches the encoded hash.
//
// Comparison is constant-time. A malformed stored hash returns false with an
// error rather than panicking: a corrupt row must fail one login, not the
// process.
func VerifyPassword(plain, encoded string) (bool, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plain), salt, p.time, p.memory, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("stored password hash is not argon2id in PHC form")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, fmt.Errorf("stored password hash has no version: %w", err)
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("stored password hash is argon2 version %d, this build speaks %d", version, argon2.Version)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, fmt.Errorf("stored password hash has unreadable parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("stored password salt is not base64: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("stored password hash is not base64: %w", err)
	}

	p.saltLen = uint32(len(salt))
	p.keyLen = uint32(len(key))
	return p, salt, key, nil
}

// dummyHash is verified against when no user matched, so that a login attempt
// for an unknown address costs the same as one for a known address. Without it,
// response time enumerates accounts.
var dummyHash = func() string {
	h, err := hashWith("zettaplanner-timing-equaliser", defaultParams)
	if err != nil {
		panic("auth: cannot build the timing equaliser hash: " + err.Error())
	}
	return h
}()
