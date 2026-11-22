package jsonwire

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

type metadata struct {
	InstanceID  string    `json:"instance_id"`
	Tool        string    `json:"tool"`
	CompletedAt time.Time `json:"completed_at"`
	DurationNS  int64     `json:"duration_ns,string"`
}

func meta(m domain.Metadata) metadata {
	return metadata{m.InstanceID, m.Tool, m.CompletedAt, int64(m.Duration)}
}
func EncodeUUID(r domain.UUIDResult) ([]byte, error) {
	return json.Marshal(struct {
		Metadata metadata `json:"metadata"`
		UUID     string   `json:"uuid"`
	}{meta(r.Metadata), r.UUID})
}
func EncodeCurve(r domain.CurveResult) ([]byte, error) {
	return json.Marshal(struct {
		Metadata   metadata        `json:"metadata"`
		PrivateKey string          `json:"private_key"`
		PublicKey  string          `json:"public_key"`
		Hash32     string          `json:"hash32"`
		Encoding   domain.Encoding `json:"encoding"`
	}{meta(r.Metadata), r.PrivateKey, r.PublicKey, r.Hash32, r.Encoding})
}
func EncodeMLDSA65(r domain.MLDSA65Result) ([]byte, error) {
	return json.Marshal(struct {
		Metadata metadata `json:"metadata"`
		Seed     string   `json:"seed"`
		Verify   string   `json:"verify"`
	}{meta(r.Metadata), r.Seed, r.Verify})
}
func EncodeMLKEM768(r domain.MLKEM768Result) ([]byte, error) {
	return json.Marshal(struct {
		Metadata metadata `json:"metadata"`
		Seed     string   `json:"seed"`
		Client   string   `json:"client"`
		Hash32   string   `json:"hash32"`
	}{meta(r.Metadata), r.Seed, r.Client, r.Hash32})
}

type pair struct {
	Decryption string `json:"decryption"`
	Encryption string `json:"encryption"`
}

func EncodeVLESS(r domain.VLESSResult) ([]byte, error) {
	return json.Marshal(struct {
		Metadata metadata `json:"metadata"`
		X25519   pair     `json:"x25519"`
		MLKEM768 pair     `json:"mlkem768"`
	}{meta(r.Metadata), pair{r.X25519.Decryption, r.X25519.Encryption}, pair{r.MLKEM768.Decryption, r.MLKEM768.Encryption}})
}
func EncodeCertificate(r domain.CertificateResult) ([]byte, error) {
	return json.Marshal(struct {
		Metadata    metadata `json:"metadata"`
		Certificate string   `json:"certificate_pem"`
		PrivateKey  string   `json:"private_key_pem"`
	}{meta(r.Metadata), r.CertificatePEM, r.PrivateKeyPEM})
}
func EncodeCertificateHashes(r domain.CertificateHashResult) ([]byte, error) {
	type hash struct {
		Index      int    `json:"index"`
		CommonName string `json:"common_name"`
		SHA256     string `json:"sha256"`
	}
	rows := make([]hash, len(r.Hashes))
	for i, row := range r.Hashes {
		rows[i] = hash{row.Index, row.CommonName, row.SHA256}
	}
	return json.Marshal(struct {
		Metadata metadata `json:"metadata"`
		Hashes   []hash   `json:"hashes"`
	}{meta(r.Metadata), rows})
}
func EncodeECH(r domain.ECHResult) ([]byte, error) {
	return json.Marshal(struct {
		Metadata    metadata         `json:"metadata"`
		ConfigLists []string         `json:"config_lists"`
		ServerKeys  string           `json:"server_keys"`
		Format      domain.ECHFormat `json:"format"`
	}{meta(r.Metadata), r.ConfigLists, r.ServerKeys, r.Format})
}
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrNotConfigured):
		return "facility_not_configured"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrClosed):
		return "facility_closed"
	case errors.Is(err, domain.ErrRejected):
		return "native_rejected"
	case errors.Is(err, domain.ErrInvalidOutput):
		return "invalid_native_output"
	default:
		return "unavailable"
	}
}
