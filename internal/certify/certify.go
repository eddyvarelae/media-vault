package certify

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Certificate is the signed proof that a source disk's contents are present
// at a destination, hashed, and verified.
type Certificate struct {
	Version       int       `json:"version"`
	IssuedAt      time.Time `json:"issued_at"`
	SourceDisk    string    `json:"source_disk"`
	FileCount     int       `json:"file_count"`
	TotalBytes    int64     `json:"total_bytes"`
	OldestVerify  time.Time `json:"oldest_verification"`
	NewestVerify  time.Time `json:"newest_verification"`
	PublicKeyHex  string    `json:"public_key_hex"`
	Files         []FileRef `json:"files"`
	SignatureHex  string    `json:"signature_hex,omitempty"`
}

type FileRef struct {
	SourcePath  string    `json:"source_path"`
	DestPath    string    `json:"dest_path"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	VerifiedAt  time.Time `json:"verified_at"`
}

// ErrNotCertifiable is returned when at least one file in the manifest is not
// in 'verified' state. The certificate is *not* generated.
var ErrNotCertifiable = errors.New("not certifiable: some files are unverified or mismatched")

// Build collects every file for `disk` from the manifest, refuses if any are
// not 'verified', and returns a signed Certificate. The signing key is loaded
// from configDir/key.pem (created on first call).
func Build(m *manifest.Manifest, disk, configDir string) (*Certificate, error) {
	entries, err := m.ListByDisk(disk)
	if err != nil {
		return nil, fmt.Errorf("list manifest: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no files in manifest for disk %q", disk)
	}

	cert := &Certificate{
		Version:    1,
		IssuedAt:   time.Now().UTC(),
		SourceDisk: disk,
	}
	for _, e := range entries {
		if e.Status != "verified" {
			return nil, fmt.Errorf("%w: %s is %q", ErrNotCertifiable, e.SourcePath, e.Status)
		}
		v := time.Unix(0, e.VerifiedAt).UTC()
		if cert.OldestVerify.IsZero() || v.Before(cert.OldestVerify) {
			cert.OldestVerify = v
		}
		if v.After(cert.NewestVerify) {
			cert.NewestVerify = v
		}
		cert.Files = append(cert.Files, FileRef{
			SourcePath: e.SourcePath,
			DestPath:   e.DestPath,
			Size:       e.Size,
			SHA256:     e.SHA256,
			VerifiedAt: v,
		})
		cert.FileCount++
		cert.TotalBytes += e.Size
	}

	priv, pub, err := loadOrCreateKey(configDir)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	cert.PublicKeyHex = fmt.Sprintf("%x", pub)

	payload, err := json.Marshal(canonical(cert))
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	sig := ed25519.Sign(priv, payload)
	cert.SignatureHex = fmt.Sprintf("%x", sig)
	return cert, nil
}

// Verify re-checks an existing certificate's signature against its embedded
// public key. Useful for proving a JSON file hasn't been tampered with.
func Verify(cert *Certificate) error {
	pub, err := hexBytes(cert.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	sig, err := hexBytes(cert.SignatureHex)
	if err != nil {
		return fmt.Errorf("signature: %w", err)
	}
	payload, err := json.Marshal(canonical(cert))
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return errors.New("signature does not match")
	}
	return nil
}

// canonical returns a copy of cert with the SignatureHex field cleared, so
// signing and verification both operate on the same byte sequence.
func canonical(c *Certificate) Certificate {
	cc := *c
	cc.SignatureHex = ""
	return cc
}

func loadOrCreateKey(configDir string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	keyPath := filepath.Join(configDir, "key.pem")
	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "ED25519 PRIVATE KEY" {
			return nil, nil, fmt.Errorf("invalid key file at %s", keyPath)
		}
		if len(block.Bytes) != ed25519.PrivateKeySize {
			return nil, nil, fmt.Errorf("unexpected key length")
		}
		priv := ed25519.PrivateKey(block.Bytes)
		return priv, priv.Public().(ed25519.PublicKey), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: priv})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write key: %w", err)
	}
	return priv, pub, nil
}

func hexBytes(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= c - '0'
			case c >= 'a' && c <= 'f':
				v |= c - 'a' + 10
			case c >= 'A' && c <= 'F':
				v |= c - 'A' + 10
			default:
				return nil, fmt.Errorf("invalid hex char %q", c)
			}
		}
		out[i] = v
	}
	return out, nil
}
