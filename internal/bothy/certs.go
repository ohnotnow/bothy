package bothy

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	certHost = "*.bothy.localhost"
	certFile = "bothy.pem"
	keyFile  = "bothy-key.pem"
	// renew the certificate when it has less than this left
	renewWithin = 30 * 24 * time.Hour
)

const mkcertSetup = `mkcert creates a certificate authority that only this Mac trusts, and uses
it to sign a certificate for *.bothy.localhost. Set it up once with:

  brew install mkcert nss
  mkcert -install

nss lets Firefox trust the certificate as well as Safari and Chrome.
mkcert -install asks for your password, because it adds the new authority to
your keychain. Restart Firefox afterwards if it was open.
`

func (a *App) certDir() string {
	return filepath.Join(a.ConfigDir, "certs")
}

// certExpiry reads when the certificate at path stops being valid.
func certExpiry(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, errors.New("no PEM certificate in " + path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

// ensureCert makes the *.bothy.localhost certificate with mkcert if bothy has
// none, or a fresh one when it has less than 30 days left. It reports whether
// it made one, because a running proxy only reads the certificate at start.
func (a *App) ensureCert() (code int, made bool) {
	cert := filepath.Join(a.certDir(), certFile)
	key := filepath.Join(a.certDir(), keyFile)

	expires, err := certExpiry(cert)
	have := err == nil && fileExists(key)
	left := expires.Sub(a.now())
	if have && left > renewWithin {
		return 0, false
	}

	if a.Mkcert == nil {
		if have && left > 0 {
			fmt.Fprintf(a.Stderr, "bothy: heads up, the bothy certificate expires in %d days, and mkcert isn't installed to renew it.\n\n%s\n", days(left), mkcertSetup)
			return 0, false
		}
		fmt.Fprintf(a.Stderr, "bothy: bothy serves apps over HTTPS, with a certificate made by mkcert,\nand mkcert isn't installed.\n\n%s\nThen run bothy start again.\n", mkcertSetup)
		return 1, false
	}
	if err := os.MkdirAll(a.certDir(), 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1, false
	}
	if have {
		fmt.Fprintf(a.Stdout, "Renewing the bothy certificate, which had %d days left\n", days(left))
	} else {
		fmt.Fprintf(a.Stdout, "Making a certificate for %s with mkcert\n", certHost)
	}
	if err := a.Mkcert("-cert-file", cert, "-key-file", key, certHost); err != nil {
		fmt.Fprintf(a.Stderr, "bothy: mkcert failed: %v\n", err)
		return 1, false
	}
	return 0, true
}

func days(d time.Duration) int {
	return max(0, int(d.Hours()/24))
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
