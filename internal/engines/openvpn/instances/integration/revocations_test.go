package integration_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNativeRevocationRejectsReconnect(t *testing.T) {
	f := newFixtureWithRevocations(t, true)
	conn, err := grpc.NewClient(f.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	access := pb.NewOpenVPNAccessServiceClient(conn)
	ops := pb.NewOpenVPNOperationsServiceClient(conn)
	ctx := t.Context()
	id := &pb.OpenVPNInstanceRequest{Id: "primary"}

	f.request(t, http.MethodPut, "/execution", nil, http.StatusServiceUnavailable)
	initial := fixtureCRL(t, f.dir, 1, false)
	f.request(t, http.MethodPut, "/revocation-list", initial, http.StatusOK)
	f.request(t, http.MethodPut, "/execution", nil, http.StatusOK)
	f.tunnel(t, "--remap-usr1", "SIGTERM")
	report, err := ops.ListSessions(ctx, id)
	if err != nil || len(report.GetSessions()) != 1 {
		t.Fatalf("sessions = %v, %v", report, err)
	}
	newList := fixtureCRL(t, f.dir, 2, true)
	updated, err := access.ReplaceRevocationList(ctx, &pb.ReplaceOpenVPNRevocationRequest{Id: id.Id, SignedPem: newList})
	if err != nil || updated.GetNumber() != "2" || updated.GetRevokedCount() != 1 {
		t.Fatalf("updated CRL = %v, %v", updated, err)
	}
	f.request(t, http.MethodPut, "/revocation-list", initial, http.StatusBadRequest)
	if got := f.request(t, http.MethodGet, "/revocation-list", nil, http.StatusOK); !bytes.Contains(got, []byte(updated.Sha256)) {
		t.Fatalf("CRL readback = %s", got)
	}
	f.ping(t)
	_, err = ops.DisconnectSession(ctx, &pb.DisconnectOpenVPNSessionRequest{
		Id: id.Id, ExecutionId: report.ExecutionId, SessionId: &report.Sessions[0].Id,
	})
	if err != nil {
		t.Fatal(err)
	}

	logCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	logs, err := ops.GetLogs(logCtx, &pb.OpenVPNLogsRequest{Id: id.Id, ExecutionId: report.ExecutionId})
	if err != nil {
		t.Fatal(err)
	}
	client := f.launchClient(t, "tun-node-denied", "--connect-retry-max", "1")
	for {
		batch, err := logs.Recv()
		if err != nil {
			output, _ := os.ReadFile(client)
			t.Fatalf("server did not record certificate revocation: %v, client output %s", err, output)
		}
		for _, entry := range batch.Entries {
			if strings.Contains(strings.ToLower(entry.Message), "certificate revoked") {
				f.request(t, http.MethodDelete, "/execution", nil, http.StatusOK)
				stored, err := access.GetRevocationList(ctx, id)
				if err != nil || stored.GetSha256() != updated.Sha256 {
					t.Fatalf("stored CRL after Stop = %v, %v", stored, err)
				}
				return
			}
		}
	}
}

func fixtureCRL(t *testing.T, dir string, number int64, revoke bool) []byte {
	t.Helper()
	certificate := func(name string) *x509.Certificate {
		t.Helper()
		block := readPEM(t, filepath.Join(dir, name+".crt"))
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	ca := certificate("ca")
	block := readPEM(t, filepath.Join(dir, "ca.key"))
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	template := &x509.RevocationList{Number: big.NewInt(number), ThisUpdate: now, NextUpdate: now.Add(time.Hour)}
	if revoke {
		template.RevokedCertificateEntries = []x509.RevocationListEntry{{SerialNumber: certificate("client").SerialNumber, RevocationTime: now}}
	}
	der, err := x509.CreateRevocationList(rand.Reader, template, ca, key.(crypto.Signer))
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}

func readPEM(t *testing.T, path string) *pem.Block {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("invalid fixture PEM")
	}
	return block
}
