//go:build linux

package localproxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func localCATrustedOS(certPath string) (bool, error) {
	return false, nil
}

func installLocalCATrustOS(certPath string) error {
	if path, err := exec.LookPath("trust"); err == nil {
		cmd := exec.Command(path, "anchor", certPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("trust anchor: %w: %s", err, out)
		}
		return nil
	}
	if path, err := exec.LookPath("update-ca-certificates"); err == nil {
		target := filepath.Join("/usr/local/share/ca-certificates", "onlava-localproxy-ca.crt")
		if err := copyCertificate(certPath, target); err != nil {
			return fmt.Errorf("copy local CA for update-ca-certificates: %w", err)
		}
		cmd := exec.Command(path)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("update-ca-certificates: %w: %s", err, out)
		}
		return nil
	}
	if path, err := exec.LookPath("update-ca-trust"); err == nil {
		target := filepath.Join("/etc/pki/ca-trust/source/anchors", "onlava-localproxy-ca.crt")
		if err := copyCertificate(certPath, target); err != nil {
			return fmt.Errorf("copy local CA for update-ca-trust: %w", err)
		}
		cmd := exec.Command(path, "extract")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("update-ca-trust extract: %w: %s", err, out)
		}
		return nil
	}
	return fmt.Errorf("no supported Linux trust installer found")
}

func copyCertificate(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
