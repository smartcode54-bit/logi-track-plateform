// Command devkeys creates the local Ed25519 signing key for JWT_SIGNING_KEY_FILE
// under deploy/dev-secrets/ (gitignored) and writes its RFC 7638 thumbprint into
// .env as JWT_ACTIVE_KID. An existing key is kept unless -force is given.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

func main() {
	keyPath := flag.String("key", "deploy/dev-secrets/jwt-ed25519.pem", "PKCS#8 PEM output")
	envPath := flag.String("env", ".env", "dotenv file that receives JWT_ACTIVE_KID")
	force := flag.Bool("force", false, "replace an existing key")
	flag.Parse()

	pub, err := loadOrCreate(*keyPath, *force)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devkeys:", err)
		os.Exit(1)
	}
	kid := Thumbprint(pub)
	lines, err := dotenv.Read(*envPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "devkeys: %s is missing; run `make env` first\n", *envPath)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devkeys:", err)
		os.Exit(1)
	}
	if _, ok := dotenv.Map(lines)["JWT_ACTIVE_KID"]; !ok {
		fmt.Fprintf(os.Stderr, "devkeys: %s has no JWT_ACTIVE_KID line; recreate it with `make env FORCE=1`\n", *envPath)
		os.Exit(1)
	}
	if err := dotenv.Write(*envPath, lines, map[string]string{"JWT_ACTIVE_KID": kid}, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "devkeys:", err)
		os.Exit(1)
	}
	fmt.Printf("devkeys: %s ready, JWT_ACTIVE_KID=%s written to %s\n", *keyPath, kid, *envPath)
}

func loadOrCreate(path string, force bool) (ed25519.PublicKey, error) {
	if b, err := os.ReadFile(path); err == nil && !force {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, fmt.Errorf("%s is not PEM", path)
		}
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s is not an Ed25519 key", path)
		}
		return priv.Public().(ed25519.PublicKey), nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// 0644 so the api container user (uid 10001) can read the bind mount; local only.
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	return pub, nil
}

// Thumbprint is the RFC 7638 JWK thumbprint of an Ed25519 public key.
func Thumbprint(pub ed25519.PublicKey) string {
	x := base64.RawURLEncoding.EncodeToString(pub)
	sum := sha256.Sum256([]byte(`{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
