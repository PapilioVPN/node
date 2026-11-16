//go:build integration && linux

package online_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
)

var onlineComparisonFile = flag.String("online-comparison-config", "", "private online-read benchmark fixture JSON file")

// common/service.proto at PasarGuard/node 739eab94a9343d9f0035baf7162796844109f4c4,
// compiled with protoc 36.2 --include_imports and no source-info option.
const onlineReferenceSchemaSHA256 = "018e65ae8244691e2d1590bd7f3024f48e0323f3a2936e53425f0df447094a91"

// onlineComparison describes an already provisioned, isolated benchmark fixture.
// A reference descriptor set is generated from the pinned upstream source with
// protoc --include_imports, never a handwritten copy of its messages. Both targets
// use dynamic messages so client decoding does not favor one generated client.
type onlineComparison struct {
	Target            string `json:"target"`
	Address           string `json:"address"`
	CAFile            string `json:"ca_file"`
	CertificateFile   string `json:"certificate_file"`
	PrivateKeyFile    string `json:"private_key_file"`
	APIKeyFile        string `json:"api_key_file"`
	ReferenceSchema   string `json:"reference_schema"`
	InstanceID        string `json:"instance_id"`
	Email             string `json:"email"`
	ExpectedOnlineIPs *int64 `json:"expected_online_ips"`
	RequestTimeout    string `json:"request_timeout"`
}

// BenchmarkOnlineObservation measures successful warmed, sequential public RPCs.
// Run with -tags=integration -run=^$ -bench=^BenchmarkOnlineObservation$ -count=5
// and -args -online-comparison-config=/private/fixture.json. The fixture must keep
// its real proxy flow alive for the entire run. Every reply is checked; errors,
// wrong identities and unexpected counts invalidate the run. ns/op is the mean
// closed-loop client latency, not a percentile or saturation throughput. Reported
// allocations belong to the client process, not Node or Xray. Server plus engine
// CPU/memory, cold connections and concurrency require separate measurements.
func BenchmarkOnlineObservation(b *testing.B) {
	if *onlineComparisonFile == "" {
		b.Fatal("-online-comparison-config is required")
	}
	file, err := os.Open(*onlineComparisonFile)
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	var cfg onlineComparison
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		b.Fatal("invalid online benchmark configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		b.Fatal("online benchmark configuration must contain one JSON document")
	}
	timeout, err := time.ParseDuration(cfg.RequestTimeout)
	if err != nil || timeout <= 0 || cfg.Address == "" || cfg.Email == "" || cfg.ExpectedOnlineIPs == nil || *cfg.ExpectedOnlineIPs < 0 {
		b.Fatal("address, email, positive timeout and nonnegative expected count are required")
	}
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		b.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		b.Fatal("invalid benchmark server CA")
	}
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	var method protoreflect.MethodDescriptor
	var nameField, countField protoreflect.Name
	ctx := b.Context()
	switch cfg.Target {
	case "papilio":
		if cfg.InstanceID == "" {
			b.Fatal("Papilio requires an exact instance ID")
		}
		certificate, err := tls.LoadX509KeyPair(cfg.CertificateFile, cfg.PrivateKeyFile)
		if err != nil {
			b.Fatal("cannot load benchmark client certificate")
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
		method = xrayv1.File_internal_bootstrap_xrayapi_proto_v1_xray_proto.Services().ByName("XrayService").Methods().ByName("GetUserOnlineStats")
		nameField, countField = "email", "online_ip_count"
	case "pasarguard":
		data, err := os.ReadFile(cfg.ReferenceSchema)
		if err != nil {
			b.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != onlineReferenceSchemaSHA256 {
			b.Fatal("reference descriptor does not match the pinned PasarGuard schema")
		}
		var schema descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(data, &schema); err != nil {
			b.Fatal("invalid pinned reference descriptor set")
		}
		files, err := protodesc.NewFiles(&schema)
		if err != nil {
			b.Fatal(err)
		}
		service, err := files.FindDescriptorByName("service.NodeService")
		if err != nil {
			b.Fatal(err)
		}
		native, ok := service.(protoreflect.ServiceDescriptor)
		if !ok {
			b.Fatal("reference NodeService is not a service")
		}
		method = native.Methods().ByName("GetUserOnlineStats")
		nameField, countField = "name", "value"
		key, err := os.ReadFile(cfg.APIKeyFile)
		if err != nil || strings.TrimSpace(string(key)) == "" {
			b.Fatal("cannot load benchmark reference API key")
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", strings.TrimSpace(string(key)))
	default:
		b.Fatal("target must be papilio or pasarguard")
	}
	if method == nil || method.IsStreamingClient() || method.IsStreamingServer() {
		b.Fatal("expected unary online observation method")
	}
	request := dynamicpb.NewMessage(method.Input())
	inputIdentity := method.Input().Fields().ByName(nameField)
	if inputIdentity == nil || inputIdentity.Kind() != protoreflect.StringKind {
		b.Fatal("unexpected online observation request contract")
	}
	if cfg.Target == "papilio" {
		request.Set(method.Input().Fields().ByName("instance_id"), protoreflect.ValueOfString(cfg.InstanceID))
	}
	request.Set(inputIdentity, protoreflect.ValueOfString(cfg.Email))
	identity := method.Output().Fields().ByName(nameField)
	count := method.Output().Fields().ByName(countField)
	if identity == nil || count == nil || identity.Kind() != protoreflect.StringKind || count.Kind() != protoreflect.Int64Kind {
		b.Fatal("unexpected online observation response contract")
	}
	connection, err := grpc.NewClient(cfg.Address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithDisableRetry())
	if err != nil {
		b.Fatal(err)
	}
	defer connection.Close()
	path := "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
	observe := func() {
		requestContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		response := dynamicpb.NewMessage(method.Output())
		if err := connection.Invoke(requestContext, path, request, response); err != nil {
			b.Fatal(err)
		}
		if response.Get(identity).String() != cfg.Email || response.Get(count).Int() != *cfg.ExpectedOnlineIPs {
			b.Fatal("unexpected online observation; benchmark fixture is not stable")
		}
		if cfg.Target == "papilio" && response.Get(method.Output().Fields().ByName("instance_id")).String() != cfg.InstanceID {
			b.Fatal("unexpected online observation instance")
		}
	}
	observe() // Establish TLS/channel/readiness before the timed interval.
	b.ReportAllocs()
	for b.Loop() {
		observe()
	}
}
