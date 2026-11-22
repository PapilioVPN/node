package process

import (
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

func TestPinnedVersionProjectionRejectsSecrets(t *testing.T) {
	got, err := parse([]byte("Xray 26.11.9 (Xray, Penetrates Everything.) 676eaf6 (go1.27.3 linux/amd64)\nA unified platform for anti-censorship.\npassword=private\n"))
	if err != nil || got.Version != "26.11.9" || got.Build != "676eaf6" || got.GoVersion != "go1.27.3" || got.Architecture != "amd64" {
		t.Fatal(got, err)
	}
	for _, line := range []string{"private password", "Xray 26.11.9 (Xray, Penetrates Everything.) password (go1.27.3 linux/amd64)", "Xray 26.11.9 (Xray, Penetrates Everything.) 676eaf6 (go1.27.3 linux/private-key)"} {
		if _, err := parse([]byte(line)); !errors.Is(err, domain.ErrMalformed) {
			t.Fatal(line, err)
		}
	}
}
