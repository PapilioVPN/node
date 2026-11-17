package settings

import (
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/shared/mtls"
)

func validateTLS(files mtls.Files) error {
	if strings.TrimSpace(files.Certificate) == "" || strings.TrimSpace(files.PrivateKey) == "" || strings.TrimSpace(files.CA) == "" {
		return errors.New("tls requires certificate, private_key and ca")
	}
	return nil
}

func resolveTLS(base string, files mtls.Files) mtls.Files {
	return mtls.Files{Certificate: relativeTo(base, files.Certificate), PrivateKey: relativeTo(base, files.PrivateKey), CA: relativeTo(base, files.CA)}
}

func relativeTo(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func validateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("address requires a numeric TCP port")
	}
	return nil
}
