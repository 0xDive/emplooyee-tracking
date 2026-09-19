package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpStep   = 30
)

var ErrInvalidMFASecret = errors.New("invalid mfa secret")

func (m *Manager) GenerateTOTPSecret() (raw []byte, encoded string, err error) {
	raw = make([]byte, 20)
	if _, err = rand.Read(raw); err != nil {
		return nil, "", err
	}
	encoded = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return raw, encoded, nil
}

func (m *Manager) EncryptMFASecret(secret []byte) ([]byte, error) {
	key := sha256.Sum256(append([]byte("actilens:mfa:v1:"), m.secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(secret)+gcm.Overhead())
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, secret, []byte("actilens-user-mfa"))
	return out, nil
}

func (m *Manager) DecryptMFASecret(ciphertext []byte) ([]byte, error) {
	key := sha256.Sum256(append([]byte("actilens:mfa:v1:"), m.secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) <= gcm.NonceSize() {
		return nil, ErrInvalidMFASecret
	}
	nonce := ciphertext[:gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], []byte("actilens-user-mfa"))
	if err != nil {
		return nil, ErrInvalidMFASecret
	}
	return plain, nil
}

func VerifyTOTP(secret []byte, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return false
	}
	counter := now.UTC().Unix() / totpStep
	for offset := int64(-1); offset <= 1; offset++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, counter+offset)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func totpCode(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod)
}

func TOTPURI(secretBase32, account, issuer string) string {
	if issuer == "" {
		issuer = "ActiLens"
	}
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secretBase32)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(totpDigits))
	q.Set("period", strconv.Itoa(totpStep))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func GenerateRecoveryCodes(count int) ([]string, []string, error) {
	if count <= 0 {
		count = 10
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	rawCodes := make([]string, 0, count)
	hashes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, err
		}
		chars := make([]byte, 10)
		for j, b := range buf {
			chars[j] = alphabet[int(b)%len(alphabet)]
		}
		code := string(chars[:5]) + "-" + string(chars[5:])
		rawCodes = append(rawCodes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return rawCodes, hashes, nil
}

func HashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte("actilens:recovery:v1:" + normalized))
	return fmt.Sprintf("%x", sum[:])
}
