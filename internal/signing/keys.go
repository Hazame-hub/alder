package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys on disk.
//
// PEM, in the encodings the standard library already reads and writes: PKCS#8
// for a private key, SPKI for a public one. Nothing here invents a format, and
// nothing here holds a passphrase: a private key is a file the operator's own
// tools protect, and Alder's part is to read it, use it once and forget it.
//
// The server reads public keys only. There is no flag, no environment variable
// and no code path that gives it a private one.

const (
	privatePEMType = "PRIVATE KEY"
	publicPEMType  = "PUBLIC KEY"
	// maxKeyFile bounds a key file: a PEM Ed25519 key is a few hundred bytes,
	// and a key file is often a path someone else chose.
	maxKeyFile = 1 << 20
)

// ErrNotAKey is returned for a file that is not an Ed25519 key Alder reads.
var ErrNotAKey = errors.New("signing: the file is not an Ed25519 key in PEM form")

// GenerateKey makes a new Ed25519 pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// EncodePrivateKey renders a private key as PEM (PKCS#8).
func EncodePrivateKey(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: privatePEMType, Bytes: der}), nil
}

// EncodePublicKey renders a public key as PEM (SPKI), with its key id in the
// header so a person can read it without running anything.
func EncodePublicKey(pub ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: publicPEMType,
		Headers: map[string]string{"alder-key-id": KeyID(pub)}, Bytes: der}), nil
}

// ParsePrivateKey reads one Ed25519 private key from PEM.
func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block", ErrNotAKey)
	}
	if block.Type != privatePEMType {
		return nil, fmt.Errorf("%w: the PEM block is %q, not %q", ErrNotAKey, block.Type, privatePEMType)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.Join(ErrNotAKey, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: it is a %T, and Alder signs with Ed25519", ErrNotAKey, parsed)
	}
	return key, nil
}

// ParsePublicKeys reads every Ed25519 public key in a PEM file, so one file
// can hold a team's keys.
func ParsePublicKeys(data []byte) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != publicPEMType {
			return nil, fmt.Errorf("%w: a PEM block is %q, not %q", ErrNotAKey, block.Type, publicPEMType)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.Join(ErrNotAKey, err)
		}
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: a key is a %T, and Alder verifies Ed25519", ErrNotAKey, parsed)
		}
		out = append(out, pub)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no public key in it", ErrNotAKey)
	}
	return out, nil
}

// LoadPrivateKey reads a private key from a file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateKey(data)
}

// LoadTrustedKeys reads the public keys a verifier trusts, from files and
// directories. A directory contributes every .pem file directly in it, sorted,
// so a deployment can drop a key in rather than edit a list.
func LoadTrustedKeys(paths []string) (TrustedKeys, error) {
	trusted := TrustedKeys{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", path, err)
		}
		files := []string{path}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, fmt.Errorf("trusted keys in %s: %w", path, err)
			}
			files = files[:0]
			for _, entry := range entries {
				if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pem") {
					continue
				}
				files = append(files, filepath.Join(path, entry.Name()))
			}
			sort.Strings(files)
			if len(files) == 0 {
				return nil, fmt.Errorf("trusted keys in %s: the directory holds no .pem file", path)
			}
		}
		for _, file := range files {
			data, err := readKeyFile(file)
			if err != nil {
				return nil, err
			}
			keys, err := ParsePublicKeys(data)
			if err != nil {
				return nil, fmt.Errorf("trusted key %s: %w", file, err)
			}
			for _, pub := range keys {
				trusted.Add(pub)
			}
		}
	}
	return trusted, nil
}

func readKeyFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxKeyFile {
		return nil, fmt.Errorf("%s: larger than a key file is", path)
	}
	// The path is an operator's own argument -- a key file they named on the
	// command line -- and reading it is the whole point of the function.
	return os.ReadFile(path) //nolint:gosec // the caller names the key file
}
