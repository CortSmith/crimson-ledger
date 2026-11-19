package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crimsonledger/internal/config"
)

const (
	tlsDirName    = "tls"
	certFileName  = "server.crt"
	keyFileName   = "server.key"
	certValidDays = 365
	serialBits    = 128
	privateFile   = 0o600
	privateDir    = 0o700
)

// hostList is a repeatable --host flag.
type hostList []string

func (h *hostList) String() string     { return strings.Join(*h, ",") }
func (h *hostList) Set(v string) error { *h = append(*h, v); return nil }

func runTLSSelfsign(args []string, io IO) int {
	fs := flag.NewFlagSet("tls-selfsign", flag.ContinueOnError)
	var hosts hostList
	fs.Var(&hosts, "host", "IP address or DNS name the certificate is valid for (repeatable)")
	force := fs.Bool("force", false, "replace an existing certificate and key")
	if code, ok := parseFlags(fs, args, io.Stderr); !ok {
		return code
	}
	if len(hosts) == 0 {
		return usageFail(io.Stderr, "tls-selfsign", "at least one --host is required, for example --host 192.168.1.20")
	}
	home, err := config.EnsureHome()
	if err != nil {
		return fail(io.Stderr, "tls-selfsign", err)
	}
	certPath, keyPath, err := writeSelfSigned(filepath.Join(home, tlsDirName), hosts, *force, io.now())
	if err != nil {
		return fail(io.Stderr, "tls-selfsign", err)
	}
	fmtLine(io, "wrote "+certPath+" and "+keyPath)
	fmtLine(io, "1. In api.json set tls_cert and tls_key to those two paths.")
	fmtLine(io, "2. Copy "+certPath+" to each machine running the logger or viewer and set ca_file to it.")
	return exitOK
}

// writeSelfSigned creates dir (0700) with server.crt and server.key (0600) and returns their paths.
func writeSelfSigned(dir string, hosts []string, force bool, now time.Time) (certPath, keyPath string, err error) {
	template, err := certTemplate(hosts, now)
	if err != nil {
		return "", "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("sign certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("encode key: %w", err)
	}
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return "", "", fmt.Errorf("create %s: %w", dir, err)
	}
	certPath, keyPath = filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)
	// Key first: a crash between the two writes leaves a key without a certificate, never the reverse.
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, force); err != nil {
		return "", "", err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, force); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func certTemplate(hosts []string, now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, fmt.Errorf("serial number: %w", err)
	}
	t := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "crimson-api"},
		NotBefore:    now.Add(-time.Hour), // tolerate a client clock slightly behind
		NotAfter:     now.Add(certValidDays * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if err := addSAN(t, h); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func addSAN(t *x509.Certificate, host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		t.IPAddresses = append(t.IPAddresses, ip.AsSlice())
		return nil
	}
	if host == "" || strings.ContainsAny(host, " /:@\t") {
		return fmt.Errorf("--host %q is neither an IP address nor a DNS name", host)
	}
	t.DNSNames = append(t.DNSNames, host)
	return nil
}

func writePEM(path, blockType string, der []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, privateFile) // #nosec G304 -- <home>/tls/<fixed name>
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; pass --force to replace it", path)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := os.Chmod(path, privateFile); err != nil { // force may reuse a looser existing file
		_ = f.Close()
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}
