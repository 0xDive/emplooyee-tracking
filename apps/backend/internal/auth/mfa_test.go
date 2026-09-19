package auth

import (
	"testing"
	"time"
)

func TestTOTPAndRecoveryCodePrimitives(t *testing.T) {
	secret := []byte("01234567890123456789")
	now := time.Unix(1_700_000_000, 0).UTC()
	code := totpCode(secret, now.Unix()/totpStep)

	if !VerifyTOTP(secret, code, now) {
		t.Fatal("valid TOTP code was rejected")
	}
	if VerifyTOTP(secret, "000000", now) && code != "000000" {
		t.Fatal("invalid TOTP code was accepted")
	}
	if !VerifyTOTP(secret, code, now.Add(time.Duration(totpStep)*time.Second)) {
		t.Fatal("adjacent TOTP step should be accepted by the configured clock-skew window")
	}

	codes, hashes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("recovery lengths codes=%d hashes=%d, want 10/10", len(codes), len(hashes))
	}
	seenCodes := map[string]bool{}
	seenHashes := map[string]bool{}
	for i, code := range codes {
		if seenCodes[code] {
			t.Fatalf("duplicate recovery code generated: %q", code)
		}
		seenCodes[code] = true
		if hashes[i] != HashRecoveryCode(code) {
			t.Fatalf("stored recovery hash does not match generated code %d", i)
		}
		if seenHashes[hashes[i]] {
			t.Fatalf("duplicate recovery hash generated at index %d", i)
		}
		seenHashes[hashes[i]] = true
	}
}

func TestMFASecretEncryptionRoundTrip(t *testing.T) {
	manager := NewManager("mfa-test-secret")
	plain := []byte("01234567890123456789")

	encrypted, err := manager.EncryptMFASecret(plain)
	if err != nil {
		t.Fatalf("encrypt mfa secret: %v", err)
	}
	if string(encrypted) == string(plain) {
		t.Fatal("encrypted MFA secret equals plaintext")
	}
	decrypted, err := manager.DecryptMFASecret(encrypted)
	if err != nil {
		t.Fatalf("decrypt mfa secret: %v", err)
	}
	if string(decrypted) != string(plain) {
		t.Fatalf("decrypted secret = %q, want original", decrypted)
	}

	encrypted[0] ^= 0xff
	if _, err := manager.DecryptMFASecret(encrypted); err == nil {
		t.Fatal("tampered MFA secret was accepted")
	}
}
