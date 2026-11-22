//go:build linux

package process

import (
	"context"
	"crypto/ecdh"
	"crypto/mlkem"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func (r *Runner) VLESSEncryption(ctx context.Context) (domain.VLESSResult, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.VLESSResult{}, err
	}
	defer release()
	raw, err := r.run(ctx, "vlessenc")
	if err != nil {
		return domain.VLESSResult{}, err
	}
	defer clear(raw)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 9 || lines[0] != "Choose one Authentication to use, do not mix them. Ephemeral key exchange is Post-Quantum safe anyway." || lines[1] != "" || lines[2] != "Authentication: X25519, not Post-Quantum" || lines[5] != "" || lines[6] != "Authentication: ML-KEM-768, Post-Quantum" {
		return domain.VLESSResult{}, domain.ErrInvalidOutput
	}
	x, err := authentication(lines[3], lines[4], 32)
	if err != nil {
		return domain.VLESSResult{}, err
	}
	pq, err := authentication(lines[7], lines[8], 64)
	if err != nil {
		return domain.VLESSResult{}, err
	}
	return domain.VLESSResult{X25519: x, MLKEM768: pq}, nil
}
func authentication(server, client string, size int) (domain.AuthenticationPair, error) {
	server, ok := strings.CutPrefix(server, `"decryption": `)
	if !ok {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	client, ok = strings.CutPrefix(client, `"encryption": `)
	if !ok {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	decryption, err := strconv.Unquote(server)
	if err != nil {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	encryption, err := strconv.Unquote(client)
	if err != nil {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	serverKey, ok := strings.CutPrefix(decryption, "mlkem768x25519plus.native.600s.")
	if !ok {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	clientKey, ok := strings.CutPrefix(encryption, "mlkem768x25519plus.native.0rtt.")
	if !ok {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	seed, err := decode(serverKey, base64.RawURLEncoding, size)
	if err != nil {
		return domain.AuthenticationPair{}, err
	}
	defer clear(seed)
	var public []byte
	if size == 32 {
		if seed[0]&7 != 0 || seed[31]&0xc0 != 0x40 {
			return domain.AuthenticationPair{}, domain.ErrInvalidOutput
		}
		key, err := ecdh.X25519().NewPrivateKey(seed)
		if err != nil {
			return domain.AuthenticationPair{}, domain.ErrInvalidOutput
		}
		public = key.PublicKey().Bytes()
	} else {
		key, err := mlkem.NewDecapsulationKey768(seed)
		if err != nil {
			return domain.AuthenticationPair{}, domain.ErrInvalidOutput
		}
		public = key.EncapsulationKey().Bytes()
	}
	if base64.RawURLEncoding.EncodeToString(public) != clientKey {
		return domain.AuthenticationPair{}, domain.ErrInvalidOutput
	}
	return domain.AuthenticationPair{Decryption: decryption, Encryption: encryption}, nil
}
