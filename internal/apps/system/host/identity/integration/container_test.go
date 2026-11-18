//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/infra/sys"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var image = flag.String("image", "", "prebuilt Node image to verify")

func TestPackagedHostIdentity(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt checked image")
	}
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki, "all")
	// Mount only the private configuration/identities. Host UTS exposes uname
	// identity without a host filesystem mount or added Linux capabilities.
	directory := filepath.Dir(pki.Server.Certificate)
	localFiles := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	config.Communications.HTTP.TLS = localFiles(pki.Server)
	config.Communications.GRPC.TLS = localFiles(pki.Server)
	config.Communications.NATS.TLS = localFiles(pki.Client)
	contents, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yml"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "node-identity-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--uts=host", "--cap-drop=ALL", "--read-only",
		"--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "--mount", "type=bind,src="+directory+",dst=/etc/node,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	addresses := containerAddresses(t, name)
	client := connectClients(t, pki.Client, config, addresses)
	want, err := (sys.Reader{}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	response, err := client.HTTP.Get(client.HTTPAddress)
	if err != nil {
		t.Fatal(err)
	}
	contents, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("packaged HTTP read failed: %v %s", err, contents)
	}
	assertObservation(t, contents, want, before)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := identityv1.NewHostIdentityServiceClient(client.GRPC).GetHostIdentity(ctx, &identityv1.GetHostIdentityRequest{})
	if err != nil || result.GetIdentity().GetHostname() != want.Hostname || result.GetIdentity().GetRelease() != want.Release {
		t.Fatalf("packaged gRPC host identity: %v %v", result, err)
	}
	message, err := client.NATS.Request(subject, []byte("{}"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertObservation(t, message.Data, want, before)
	docker(t, "stop", "--time", "6", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged capability did not shut down cleanly")
	}
}
