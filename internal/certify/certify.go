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
	"strings"
	"syscall"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Certificate is the signed proof that a source disk's contents are present
// at a destination, hashed, and verified.
type Certificate struct {
	Version      int       `json:"version"`
	IssuedAt     time.Time `json:"issued_at"`
	SourceDisk   string    `json:"source_disk"`
	FileCount    int       `json:"file_count"`
	TotalBytes   int64     `json:"total_bytes"`
	OldestVerify time.Time `json:"oldest_verification"`
	NewestVerify time.Time `json:"newest_verification"`
	PublicKeyHex string    `json:"public_key_hex"`
	Files        []FileRef `json:"files"`
	SignatureHex string    `json:"signature_hex,omitempty"`
}

type FileRef struct {
	SourcePath string    `json:"source_path"`
	DestPath   string    `json:"dest_path"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	VerifiedAt time.Time `json:"verified_at"`
}

// ErrInsideArchive is returned when the certificate would be written into
// the tree it certifies.
var ErrInsideArchive = errors.New("certificate output is inside the archive it certifies")

// ErrOutputNotAFile is returned when something other than a regular file
// already sits at the output path.
var ErrOutputNotAFile = errors.New("certificate output path is not a regular file")

// CheckOutput is every placement check a certificate output path must pass
// before anything is signed. Physical, in this order:
//
//  1. The leaf itself: os.WriteFile follows a symlink, so a link at the
//     output name - dangling or not - would write wherever it points
//     (a verified photo, a path inside the tree). Lstat; a symlink, a
//     directory or anything but a regular file or nothing is refused.
//  2. Containment under root when the caller knows it (--root): resolved
//     paths, component-wise. This is the real check; a tree whose files
//     have all been damaged is still the tree.
//  3. Otherwise the tree is recognised by its contents (InsideArchive) -
//     a fallback for callers that do not pass --root, and only as good as
//     the files still matching their rows.
//
// The returned string names the root the output was found inside, "" if
// clear.
func CheckOutput(out, root string, rows []manifest.Entry) (string, error) {
	if fi, err := os.Lstat(out); err == nil {
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%w: %s is %s", ErrOutputNotAFile, out, describe(fi))
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if root != "" {
		in, err := under(root, out)
		if err != nil {
			return "", err
		}
		if in {
			r, _ := filepath.Abs(root)
			return r, nil
		}
	}
	return InsideArchive(out, rows)
}

// under reports whether path lies under root, both made absolute and
// resolved (path by its longest existing ancestor), compared with
// filepath.Rel so "." and "/" roots work.
func under(root, path string) (bool, error) {
	r, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	if rr, err := filepath.EvalSymlinks(r); err == nil {
		r = rr
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	p = resolveExisting(p)
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false, nil
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// resolveExisting resolves symlinks in the longest existing ancestor of p
// and re-appends the rest, so a path that does not exist yet still gets
// the physical directory it would land in.
func resolveExisting(p string) string {
	dir, rest := p, ""
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(dir) == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
}

func describe(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "a symlink"
	case fi.IsDir():
		return "a directory"
	default:
		return fi.Mode().String()
	}
}

// WriteOutput writes data to out without ever following what is at out.
// os.WriteFile opens the leaf itself, so a symlink substituted there
// between CheckOutput and the write would be followed - into a verified
// photo, or into the tree. Instead the bytes go to a temporary name beside
// out, created O_CREATE|O_EXCL|O_NOFOLLOW (nothing that exists is truncated
// and no link at the temp name is followed), fsynced, and renamed over the
// leaf: rename replaces whatever directory entry is at out, symlink or
// file, and follows nothing. What this does not bind is the parent
// directory itself between check and write; binding that needs openat
// (os.Root, Go 1.25) and is noted in the backlog.
func WriteOutput(out string, data []byte) error {
	tmp := out + ".vault-partial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w (a leftover from an interrupted run? look, then remove it by hand)", tmp, err)
	}
	cleanup := func() { os.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		cleanup()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, out); err != nil {
		cleanup()
		return fmt.Errorf("rename %s: %w", out, err)
	}
	return nil
}

// InsideArchive reports the ancestor directory of out under which one of
// the disk's archived files physically exists - i.e. the destination root,
// or a directory below it - or "" when out is clear of the tree. certify
// takes no destination root, so the tree is recognised by its contents:
// every ancestor of out is tried as a root for every row's dest_path,
// Lstat only, regular file, size equal. The check stops at the first hit.
//
// Why it matters (B25): a certificate written into its own tree is a file
// the next scan finds with no row - it once blocked 39,219 files as a
// collision - and a cert row would then attest itself. Certificates live
// beside the manifest, not beside the footage.
func InsideArchive(out string, rows []manifest.Entry) (string, error) {
	abs, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	// Physical, not spelled: the directory the file would land in, with
	// symlinks in its existing part resolved.
	dir := filepath.Dir(resolveExisting(abs))
	for anc := dir; ; anc = filepath.Dir(anc) {
		for _, e := range rows {
			if e.DestPath == "" {
				continue
			}
			fi, err := os.Lstat(filepath.Join(anc, e.DestPath))
			if err == nil && fi.Mode().IsRegular() && fi.Size() == e.Size {
				return anc, nil
			}
		}
		if filepath.Dir(anc) == anc {
			return "", nil
		}
	}
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
