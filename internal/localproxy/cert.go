package localproxy

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	localProxyCACertFile   = "onlava-local-ca.crt.pem"
	localProxyCAKeyFile    = "onlava-local-ca.key.pem"
	localProxyLeafCertFile = "onlava-local-leaf.crt.pem"
	localProxyLeafKeyFile  = "onlava-local-leaf.key.pem"
)

type localCertificates struct {
	Leaf   tls.Certificate
	CAPath string
	CACert *x509.Certificate
}

func prepareLocalCertificates(subjects []string) (localCertificates, error) {
	subjects = normalizeCertificateSubjects(subjects)
	if len(subjects) == 0 {
		return localCertificates{}, fmt.Errorf("local HTTPS proxy requires at least one certificate subject")
	}
	dir, err := localProxyCacheDir()
	if err != nil {
		return localCertificates{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return localCertificates{}, err
	}
	_ = os.Chmod(dir, 0o700)

	paths := certificatePaths{
		caCert:   filepath.Join(dir, localProxyCACertFile),
		caKey:    filepath.Join(dir, localProxyCAKeyFile),
		leafCert: filepath.Join(dir, localProxyLeafCertFile),
		leafKey:  filepath.Join(dir, localProxyLeafKeyFile),
	}
	caCert, caKey, err := loadOrCreateCA(paths)
	if err != nil {
		return localCertificates{}, err
	}
	leaf, err := loadOrCreateLeaf(paths, caCert, caKey, subjects)
	if err != nil {
		return localCertificates{}, err
	}
	return localCertificates{Leaf: leaf, CAPath: paths.caCert, CACert: caCert}, nil
}

type certificatePaths struct {
	caCert   string
	caKey    string
	leafCert string
	leafKey  string
}

func localProxyCacheDir() (string, error) {
	root := os.Getenv("ONLAVA_DEV_CACHE_DIR")
	if root == "" {
		var err error
		root, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate user cache directory: %w", err)
		}
	}
	return filepath.Join(root, "onlava", "localproxy"), nil
}

func loadOrCreateCA(paths certificatePaths) (*x509.Certificate, crypto.Signer, error) {
	if cert, key, err := loadCertificateAndKey(paths.caCert, paths.caKey); err == nil && validCA(cert, key) {
		return cert, key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "onlava Development Local Proxy CA",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEMFile(paths.caCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEMFile(paths.caKey, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func loadOrCreateLeaf(paths certificatePaths, caCert *x509.Certificate, caKey crypto.Signer, subjects []string) (tls.Certificate, error) {
	if cert, key, err := loadCertificateAndKey(paths.leafCert, paths.leafKey); err == nil && validLeaf(cert, key, caCert, subjects) {
		certPEM, err := os.ReadFile(paths.leafCert)
		if err != nil {
			return tls.Certificate{}, err
		}
		keyPEM, err := os.ReadFile(paths.leafKey)
		if err != nil {
			return tls.Certificate{}, err
		}
		tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return tls.Certificate{}, err
		}
		tlsCert.Leaf = cert
		return tlsCert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	serial, err := randomSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "onlava Local Proxy",
		},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(90 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, subject := range subjects {
		if ip := net.ParseIP(subject); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, subject)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, key.Public(), caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writePEMFile(paths.leafCert, certPEM); err != nil {
		return tls.Certificate{}, err
	}
	if err := writePEMFile(paths.leafKey, keyPEM); err != nil {
		return tls.Certificate{}, err
	}
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(tlsCert.Certificate) > 0 {
		tlsCert.Leaf, _ = x509.ParseCertificate(tlsCert.Certificate[0])
	}
	return tlsCert, nil
}

func loadCertificateAndKey(certPath, keyPath string) (*x509.Certificate, crypto.Signer, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cert, err := parseCertificatePEM(certPEM)
	if err != nil {
		return nil, nil, err
	}
	key, err := parsePrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func parseCertificatePEM(data []byte) (*x509.Certificate, error) {
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}
	return nil, fmt.Errorf("missing certificate PEM block")
}

func parsePrivateKeyPEM(data []byte) (crypto.Signer, error) {
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		switch block.Type {
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "PRIVATE KEY":
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			signer, ok := key.(crypto.Signer)
			if !ok {
				return nil, fmt.Errorf("private key is not a signer")
			}
			return signer, nil
		}
	}
	return nil, fmt.Errorf("missing private key PEM block")
}

func validCA(cert *x509.Certificate, key crypto.Signer) bool {
	if cert == nil || key == nil || !cert.IsCA || !cert.BasicConstraintsValid {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || cert.NotAfter.Sub(now) < 30*24*time.Hour {
		return false
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return false
	}
	return publicKeysEqual(cert.PublicKey, key.Public())
}

func validLeaf(cert *x509.Certificate, key crypto.Signer, caCert *x509.Certificate, subjects []string) bool {
	if cert == nil || key == nil || caCert == nil {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || cert.NotAfter.Sub(now) < 7*24*time.Hour {
		return false
	}
	if err := cert.CheckSignatureFrom(caCert); err != nil {
		return false
	}
	if !publicKeysEqual(cert.PublicKey, key.Public()) {
		return false
	}
	for _, subject := range subjects {
		if err := cert.VerifyHostname(subject); err != nil {
			return false
		}
	}
	return true
}

func publicKeysEqual(a, b any) bool {
	aDER, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false
	}
	bDER, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aDER, bDER)
}

func normalizeCertificateSubjects(subjects []string) []string {
	out := []string{}
	for _, subject := range subjects {
		subject = normalizeHost(subject)
		if subject == "" {
			continue
		}
		seen := false
		for _, existing := range out {
			if existing == subject {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, subject)
		}
	}
	return out
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func writePEMFile(path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d.tmp", filepath.Base(path), time.Now().UnixNano()))
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(tmp)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

var _ crypto.Signer = (*ecdsa.PrivateKey)(nil)
var _ crypto.Signer = (*rsa.PrivateKey)(nil)
