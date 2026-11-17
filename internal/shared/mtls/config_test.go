package mtls_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRejectsInvalidOrMissingIdentityMaterial(t *testing.T) {
	pki := testutil.NewPKI(t)
	badCA := filepath.Join(t.TempDir(), "invalid-ca.crt")
	if err := os.WriteFile(badCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, files := range []mtls.Files{
		{},
		{Certificate: pki.Server.Certificate, PrivateKey: pki.Client.PrivateKey, CA: pki.Server.CA},
		{Certificate: pki.Server.Certificate, PrivateKey: pki.Server.PrivateKey, CA: badCA},
	} {
		if _, err := mtls.Server(files); err == nil {
			t.Fatal("invalid server material accepted")
		}
		if _, err := mtls.Client(files); err == nil {
			t.Fatal("invalid client material accepted")
		}
	}
}
