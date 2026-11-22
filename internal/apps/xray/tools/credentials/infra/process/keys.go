//go:build linux

package process

import (
	"context"
	"crypto/ecdh"
	"crypto/mldsa"
	"crypto/mlkem"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	"lukechampine.com/blake3"
)

func fields(raw []byte, labels ...string) ([]string, error) {
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(labels) {
		return nil, domain.ErrInvalidOutput
	}
	values := make([]string, len(labels))
	for i, label := range labels {
		value, ok := strings.CutPrefix(lines[i], label+": ")
		if !ok || value == "" {
			return nil, domain.ErrInvalidOutput
		}
		values[i] = value
	}
	return values, nil
}
func decode(value string, encoding *base64.Encoding, size int) ([]byte, error) {
	raw, err := encoding.Strict().DecodeString(value)
	if err != nil || len(raw) != size || encoding.EncodeToString(raw) != value {
		return nil, domain.ErrInvalidOutput
	}
	return raw, nil
}
func digest(public []byte, encoded string, encoding *base64.Encoding) error {
	hash := blake3.Sum256(public)
	if encoding.EncodeToString(hash[:]) != encoded {
		return domain.ErrInvalidOutput
	}
	return nil
}
func (r *Runner) UUID(ctx context.Context, input domain.UUIDInput) (domain.UUIDResult, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.UUIDResult{}, err
	}
	defer release()
	args := []string{"uuid"}
	if input.Name != nil {
		args = append(args, "-i", *input.Name)
	}
	raw, err := r.run(ctx, args...)
	if err != nil {
		return domain.UUIDResult{}, err
	}
	defer clear(raw)
	value := strings.TrimSuffix(string(raw), "\n")
	compact := strings.ReplaceAll(value, "-", "")
	binary, err := hex.DecodeString(compact)
	if err != nil || len(value) != 36 || len(binary) != 16 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || strings.ToLower(value) != value || binary[8]&0xc0 != 0x80 {
		return domain.UUIDResult{}, domain.ErrInvalidOutput
	}
	if input.Name == nil {
		if binary[6]>>4 != 4 {
			return domain.UUIDResult{}, domain.ErrInvalidOutput
		}
	} else {
		h := sha1.New()
		h.Write(make([]byte, 16))
		h.Write([]byte(*input.Name))
		expected := h.Sum(nil)[:16]
		expected[6] = expected[6]&15 | 0x50
		expected[8] = expected[8]&63 | 0x80
		if hex.EncodeToString(expected) != compact {
			return domain.UUIDResult{}, domain.ErrInvalidOutput
		}
	}
	return domain.UUIDResult{UUID: value}, nil
}
func (r *Runner) X25519(ctx context.Context, input domain.X25519Input) (domain.CurveResult, error) {
	args := []string{"x25519"}
	encoding := base64.RawURLEncoding
	if input.Encoding == domain.Standard {
		args = append(args, "--std-encoding")
		encoding = base64.StdEncoding
	}
	if input.PrivateKey != nil {
		args = append(args, "-i", *input.PrivateKey)
	}
	return r.curve(ctx, input.PrivateKey, input.Encoding, encoding, args)
}
func (r *Runner) WireGuard(ctx context.Context, input domain.WireGuardInput) (domain.CurveResult, error) {
	args := []string{"wg"}
	if input.PrivateKey != nil {
		args = append(args, "-i", *input.PrivateKey)
	}
	return r.curve(ctx, input.PrivateKey, domain.Standard, base64.StdEncoding, args)
}
func (r *Runner) curve(ctx context.Context, input *string, kind domain.Encoding, encoding *base64.Encoding, args []string) (domain.CurveResult, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.CurveResult{}, err
	}
	defer release()
	raw, err := r.run(ctx, args...)
	if err != nil {
		return domain.CurveResult{}, err
	}
	defer clear(raw)
	values, err := fields(raw, "PrivateKey", "Password (PublicKey)", "Hash32")
	if err != nil {
		return domain.CurveResult{}, err
	}
	private, err := decode(values[0], encoding, 32)
	if err != nil {
		return domain.CurveResult{}, err
	}
	defer clear(private)
	public, err := decode(values[1], encoding, 32)
	if err != nil {
		return domain.CurveResult{}, err
	}
	if private[0]&7 != 0 || private[31]&0xc0 != 0x40 {
		return domain.CurveResult{}, domain.ErrInvalidOutput
	}
	if input != nil {
		expected, e := encoding.DecodeString(*input)
		defer clear(expected)
		if e != nil || len(expected) != 32 {
			return domain.CurveResult{}, domain.ErrInvalidOutput
		}
		expected[0] &= 248
		expected[31] &= 127
		expected[31] |= 64
		if !equal(expected, private) {
			return domain.CurveResult{}, domain.ErrInvalidOutput
		}
	}
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil || !equal(key.PublicKey().Bytes(), public) || digest(public, values[2], encoding) != nil {
		return domain.CurveResult{}, domain.ErrInvalidOutput
	}
	return domain.CurveResult{PrivateKey: values[0], PublicKey: values[1], Hash32: values[2], Encoding: kind}, nil
}
func equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

func seedArgs(command string, input domain.SeedInput) []string {
	args := []string{command}
	if input.Seed != nil {
		args = append(args, "-i", *input.Seed)
	}
	return args
}
func (r *Runner) MLDSA65(ctx context.Context, input domain.SeedInput) (domain.MLDSA65Result, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	defer release()
	raw, err := r.run(ctx, seedArgs("mldsa65", input)...)
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	defer clear(raw)
	values, err := fields(raw, "Seed", "Verify")
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	seed, err := decode(values[0], base64.RawURLEncoding, 32)
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	defer clear(seed)
	if input.Seed != nil && values[0] != *input.Seed {
		return domain.MLDSA65Result{}, domain.ErrInvalidOutput
	}
	key, err := mldsa.NewPrivateKey(mldsa.MLDSA65(), seed)
	if err != nil || base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) != values[1] {
		return domain.MLDSA65Result{}, domain.ErrInvalidOutput
	}
	return domain.MLDSA65Result{Seed: values[0], Verify: values[1]}, nil
}
func (r *Runner) MLKEM768(ctx context.Context, input domain.SeedInput) (domain.MLKEM768Result, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	defer release()
	raw, err := r.run(ctx, seedArgs("mlkem768", input)...)
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	defer clear(raw)
	values, err := fields(raw, "Seed", "Client", "Hash32")
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	seed, err := decode(values[0], base64.RawURLEncoding, 64)
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	defer clear(seed)
	if input.Seed != nil && values[0] != *input.Seed {
		return domain.MLKEM768Result{}, domain.ErrInvalidOutput
	}
	key, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil || base64.RawURLEncoding.EncodeToString(key.EncapsulationKey().Bytes()) != values[1] || digest(key.EncapsulationKey().Bytes(), values[2], base64.RawURLEncoding) != nil {
		return domain.MLKEM768Result{}, domain.ErrInvalidOutput
	}
	return domain.MLKEM768Result{Seed: values[0], Client: values[1], Hash32: values[2]}, nil
}
