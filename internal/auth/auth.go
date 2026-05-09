package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Fingerprint returns the SHA256 fingerprint of an SSH public key in
// the format "SHA256:<base64>" matching `ssh-keygen -lf` output.
func Fingerprint(k ssh.PublicKey) string {
	sum := sha256.Sum256(k.Marshal())
	enc := base64.RawStdEncoding.EncodeToString(sum[:])
	return "SHA256:" + enc
}

// ShortFingerprint returns the last 8 hex chars of the SHA256 of the
// marshaled key, suitable for display.
func ShortFingerprint(k ssh.PublicKey) string {
	sum := sha256.Sum256(k.Marshal())
	hex := strings.ToLower(toHex(sum[:]))
	return hex[len(hex)-8:]
}

const hexDigits = "0123456789abcdef"

func toHex(b []byte) string {
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0xF]
	}
	return string(out)
}

// AuthorizedKey returns the authorized_keys-style line for a public key.
func AuthorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// ParseAuthorizedKey parses one authorized_keys-style line.
// Returns the parsed key and its SHA256 fingerprint, or an error.
func ParseAuthorizedKey(line string) (ssh.PublicKey, string, error) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, "", err
	}
	return k, Fingerprint(k), nil
}
