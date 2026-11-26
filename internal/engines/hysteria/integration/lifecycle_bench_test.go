package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	configsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/configs/bridge/grpc/proto/v1"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
)

func BenchmarkLifecycle(b *testing.B) {
	f := setup(b, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn := f.grpcConn(b)
	rpc := v1.NewHysteriaInstancesServiceClient(conn)
	validate := configsv1.NewHysteriaConfigsServiceClient(conn)
	f.request(b, http.MethodPut, "/execution", nil, 200)
	request := &v1.HysteriaInstanceRequest{Id: "edge"}
	start := &v1.StartHysteriaRequest{Id: "edge", ConfigYaml: f.source}
	validation := &configsv1.ValidateHysteriaConfigRequest{Id: "edge", ConfigYaml: f.source}
	for _, item := range []struct {
		name string
		http func(testing.TB)
		grpc func() error
	}{
		{"Status", func(t testing.TB) { f.request(t, http.MethodGet, "", nil, 200) }, func() error { _, err := rpc.GetStatus(ctx, request); return err }},
		{"SameSourceStart", func(t testing.TB) { f.request(t, http.MethodPut, "/execution", f.source, 200) }, func() error { _, err := rpc.Start(ctx, start); return err }},
		{"Validation", func(t testing.TB) { f.request(t, http.MethodPost, "/config-validations", f.source, 200) }, func() error { _, err := validate.ValidateConfig(ctx, validation); return err }},
	} {
		b.Run(item.name+"/HTTP", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				item.http(b)
			}
		})
		b.Run(item.name+"/GRPC", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := item.grpc(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	for _, transport := range []string{"HTTP", "GRPC"} {
		b.Run("ColdStartStop/"+transport, func(b *testing.B) {
			f.request(b, http.MethodDelete, "/execution", nil, 200)
			b.ReportAllocs()
			for b.Loop() {
				if transport == "HTTP" {
					f.request(b, http.MethodPut, "/execution", f.source, 200)
					f.request(b, http.MethodDelete, "/execution", nil, 200)
				} else {
					if _, err := rpc.Start(ctx, start); err != nil {
						b.Fatal(err)
					}
					if _, err := rpc.Stop(ctx, request); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
