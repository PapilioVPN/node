package integration_test

import (
	"bytes"
	"net/http"
	"os/exec"
	"testing"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func BenchmarkNativeManagement(b *testing.B) {
	f := newFixture(b)
	conn, err := grpc.NewClient(f.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	instances := pb.NewOpenVPNInstancesServiceClient(conn)
	configs := pb.NewOpenVPNConfigsServiceClient(conn)
	ops := pb.NewOpenVPNOperationsServiceClient(conn)
	f.request(b, http.MethodPut, "/execution", nil, http.StatusOK)
	f.tunnel(b)
	ctx := b.Context()
	id := &pb.OpenVPNInstanceRequest{Id: "primary"}
	validate := &pb.ValidateOpenVPNConfigRequest{Id: "primary", ConfigText: f.source}
	env := []struct {
		name string
		run  func(*testing.B)
	}{
		{"Validate/direct", func(b *testing.B) {
			cmd := exec.CommandContext(ctx, f.executable, "--config", "stdin", "--show-ciphers")
			cmd.Dir, cmd.Stdin = f.dir, bytes.NewReader(f.source)
			if err := cmd.Run(); err != nil {
				b.Fatal(err)
			}
		}},
		{"Validate/http", func(b *testing.B) {
			response := f.request(b, http.MethodPost, "/config-validations", f.source, http.StatusOK)
			if !bytes.Contains(response, []byte(`"valid":true`)) {
				b.Fatalf("validation = %s", response)
			}
		}},
		{"Validate/grpc", func(b *testing.B) {
			response, err := configs.ValidateConfig(ctx, validate)
			if err != nil || !response.GetValid() {
				b.Fatalf("validation = %v, %v", response, err)
			}
		}},
		{"Status/http", func(b *testing.B) {
			f.request(b, http.MethodGet, "", nil, http.StatusOK)
		}},
		{"Status/grpc", func(b *testing.B) {
			response, err := instances.GetStatus(ctx, id)
			if err != nil || response.GetState() != pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_READY {
				b.Fatalf("status = %v, %v", response, err)
			}
		}},
		{"Sessions/http", func(b *testing.B) {
			if report := f.request(b, http.MethodGet, "/sessions", nil, http.StatusOK); !bytes.Contains(report, []byte("fixture-client")) {
				b.Fatalf("sessions = %s", report)
			}
		}},
		{"Sessions/grpc", func(b *testing.B) {
			report, err := ops.ListSessions(ctx, id)
			if err != nil || len(report.GetSessions()) != 1 {
				b.Fatalf("sessions = %v, %v", report, err)
			}
		}},
	}
	for _, workload := range env {
		b.Run(workload.name, func(b *testing.B) {
			workload.run(b)
			b.ReportAllocs()
			for b.Loop() {
				workload.run(b)
			}
		})
	}
}
