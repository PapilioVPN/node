// Package grpcapi exposes the versioned native Xray credential service.
package grpcapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	credentialsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

type Handler struct {
	credentialsv1.UnimplementedCredentialsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	credentialsv1.RegisterCredentialsServiceServer(registrar, h)
}
func (h Handler) GenerateUUID(ctx context.Context, r *credentialsv1.GenerateUUIDRequest) (*credentialsv1.UUIDArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.UUIDInput{Name: r.Name}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.UUID(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	return &credentialsv1.UUIDArtifact{Metadata: metadata(result.Metadata), Uuid: result.UUID}, nil
}
func (h Handler) GenerateX25519(ctx context.Context, r *credentialsv1.GenerateX25519Request) (*credentialsv1.CurveArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	encoding := domain.RawURL
	switch r.Encoding {
	case credentialsv1.KeyEncoding_KEY_ENCODING_UNSPECIFIED, credentialsv1.KeyEncoding_KEY_ENCODING_BASE64URL:
	case credentialsv1.KeyEncoding_KEY_ENCODING_BASE64:
		encoding = domain.Standard
	default:
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.X25519Input{PrivateKey: r.PrivateKey, Encoding: encoding}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.X25519(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	encodingResult := credentialsv1.KeyEncoding_KEY_ENCODING_BASE64URL
	if result.Encoding == domain.Standard {
		encodingResult = credentialsv1.KeyEncoding_KEY_ENCODING_BASE64
	}
	return &credentialsv1.CurveArtifact{Metadata: metadata(result.Metadata), PrivateKey: result.PrivateKey, PublicKey: result.PublicKey, Hash32: result.Hash32, Encoding: encodingResult}, nil
}
func (h Handler) GenerateWireGuard(ctx context.Context, r *credentialsv1.GenerateWireGuardRequest) (*credentialsv1.CurveArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.WireGuardInput{PrivateKey: r.PrivateKey}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.WireGuard(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	encodingResult := credentialsv1.KeyEncoding_KEY_ENCODING_BASE64URL
	if result.Encoding == domain.Standard {
		encodingResult = credentialsv1.KeyEncoding_KEY_ENCODING_BASE64
	}
	return &credentialsv1.CurveArtifact{Metadata: metadata(result.Metadata), PrivateKey: result.PrivateKey, PublicKey: result.PublicKey, Hash32: result.Hash32, Encoding: encodingResult}, nil
}
func (h Handler) GenerateMLDSA65(ctx context.Context, r *credentialsv1.GenerateMLDSA65Request) (*credentialsv1.MLDSA65Artifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.SeedInput{Seed: r.Seed}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.MLDSA65(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	return &credentialsv1.MLDSA65Artifact{Metadata: metadata(result.Metadata), Seed: result.Seed, Verify: result.Verify}, nil
}
func (h Handler) GenerateMLKEM768(ctx context.Context, r *credentialsv1.GenerateMLKEM768Request) (*credentialsv1.MLKEM768Artifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.SeedInput{Seed: r.Seed}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.MLKEM768(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	return &credentialsv1.MLKEM768Artifact{Metadata: metadata(result.Metadata), Seed: result.Seed, Client: result.Client, Hash32: result.Hash32}, nil
}
func (h Handler) GenerateVLESSEncryption(ctx context.Context, r *credentialsv1.GenerateVLESSEncryptionRequest) (*credentialsv1.VLESSArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}

	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.VLESSEncryption(ctx, r.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	return &credentialsv1.VLESSArtifact{Metadata: metadata(result.Metadata), X25519: &credentialsv1.AuthenticationPair{Decryption: result.X25519.Decryption, Encryption: result.X25519.Encryption}, Mlkem768: &credentialsv1.AuthenticationPair{Decryption: result.MLKEM768.Decryption, Encryption: result.MLKEM768.Encryption}}, nil
}
func (h Handler) GenerateCertificate(ctx context.Context, r *credentialsv1.GenerateCertificateRequest) (*credentialsv1.CertificateArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.CertificateInput{Domains: r.Domains, CommonName: r.CommonName, Organization: r.Organization, Authority: r.Authority}
	if r.ValidForNs != nil {
		duration := time.Duration(*r.ValidForNs)
		input.ValidFor = &duration
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Certificate(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	return &credentialsv1.CertificateArtifact{Metadata: metadata(result.Metadata), CertificatePem: result.CertificatePEM, PrivateKeyPem: result.PrivateKeyPEM}, nil
}
func (h Handler) GetCertificateHashes(ctx context.Context, r *credentialsv1.GetCertificateHashesRequest) (*credentialsv1.CertificateHashes, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.CertificateHashInput{Certificate: r.Certificate}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.CertificateHashes(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	rows := make([]*credentialsv1.CertificateHash, len(result.Hashes))
	for i, row := range result.Hashes {
		rows[i] = &credentialsv1.CertificateHash{Index: uint32(row.Index), CommonName: row.CommonName, Sha256: row.SHA256}
	}
	return &credentialsv1.CertificateHashes{Metadata: metadata(result.Metadata), Hashes: rows}, nil
}
func (h Handler) GenerateECH(ctx context.Context, r *credentialsv1.GenerateECHRequest) (*credentialsv1.ECHArtifact, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	format := domain.Base64
	switch r.Format {
	case credentialsv1.ECHFormat_ECH_FORMAT_UNSPECIFIED, credentialsv1.ECHFormat_ECH_FORMAT_BASE64:
	case credentialsv1.ECHFormat_ECH_FORMAT_PEM:
		format = domain.PEM
	default:
		return nil, failure(domain.ErrInvalidArgument)
	}
	input := domain.ECHInput{ServerName: r.ServerName, ServerKeys: r.ServerKeys, Format: format}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.ECH(ctx, r.InstanceId, input)
	if err != nil {
		return nil, failure(err)
	}
	formatResult := credentialsv1.ECHFormat_ECH_FORMAT_BASE64
	if result.Format == domain.PEM {
		formatResult = credentialsv1.ECHFormat_ECH_FORMAT_PEM
	}
	return &credentialsv1.ECHArtifact{Metadata: metadata(result.Metadata), ConfigLists: result.ConfigLists, ServerKeys: result.ServerKeys, Format: formatResult}, nil
}
func metadata(m domain.Metadata) *credentialsv1.ArtifactMetadata {
	return &credentialsv1.ArtifactMetadata{InstanceId: m.InstanceID, Tool: m.Tool, CompletedAt: timestamppb.New(m.CompletedAt), DurationNs: int64(m.Duration)}
}
func failure(err error) error {
	message := jsonwire.ErrorCode(err)
	code := codes.Unavailable
	switch message {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "facility_not_configured", "native_rejected":
		code = codes.FailedPrecondition
	case "resource_exhausted":
		code = codes.ResourceExhausted
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	case "canceled":
		code = codes.Canceled
	}
	return status.Error(code, message)
}
