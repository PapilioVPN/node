//go:build linux

package process

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func vector16(data *[]byte) ([]byte, bool) {
	if len(*data) < 2 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint16(*data))
	*data = (*data)[2:]
	if n > len(*data) {
		return nil, false
	}
	value := (*data)[:n]
	*data = (*data)[n:]
	return value, true
}

// echLists validates the pinned native key-set framing and X25519 association.
// It preserves each original config as its own standard ECHConfigList. The
// pinned CLI concatenates individual lists for a multi-key restore.
func echLists(keys []byte) ([][]byte, error) {
	var lists [][]byte
	for len(keys) > 0 {
		private, ok := vector16(&keys)
		if !ok || len(private) != 32 {
			return nil, domain.ErrInvalidOutput
		}
		config, ok := vector16(&keys)
		if !ok || len(config) < 4 || binary.BigEndian.Uint16(config) != 0xfe0d {
			return nil, domain.ErrInvalidOutput
		}
		body := config[2:]
		body, ok = vector16(&body)
		if !ok || len(body) < 3 || int(binary.BigEndian.Uint16(config[2:])) != len(config)-4 || binary.BigEndian.Uint16(body[1:3]) != 0x20 {
			return nil, domain.ErrInvalidOutput
		}
		body = body[3:]
		public, ok := vector16(&body)
		if !ok || len(public) != 32 {
			return nil, domain.ErrInvalidOutput
		}
		key, err := ecdh.X25519().NewPrivateKey(private)
		if err != nil || !equal(key.PublicKey().Bytes(), public) {
			return nil, domain.ErrInvalidOutput
		}
		suites, ok := vector16(&body)
		if !ok || len(suites) == 0 || len(suites)%4 != 0 {
			return nil, domain.ErrInvalidOutput
		}
		for len(suites) > 0 {
			kdf, aead := binary.BigEndian.Uint16(suites), binary.BigEndian.Uint16(suites[2:])
			if kdf < 1 || kdf > 3 || aead < 1 || aead > 3 {
				return nil, domain.ErrInvalidOutput
			}
			suites = suites[4:]
		}
		if len(body) < 2 {
			return nil, domain.ErrInvalidOutput
		}
		nameLength := int(body[1])
		body = body[2:]
		if nameLength > len(body) || !validators.DNS(string(body[:nameLength]), false) {
			return nil, domain.ErrInvalidOutput
		}
		body = body[nameLength:]
		extensions, ok := vector16(&body)
		if !ok || len(body) != 0 {
			return nil, domain.ErrInvalidOutput
		}
		for len(extensions) > 0 {
			if len(extensions) < 2 {
				return nil, domain.ErrInvalidOutput
			}
			extensions = extensions[2:]
			if _, ok := vector16(&extensions); !ok {
				return nil, domain.ErrInvalidOutput
			}
		}

		list := make([]byte, 2+len(config))
		binary.BigEndian.PutUint16(list, uint16(len(config)))
		copy(list[2:], config)
		lists = append(lists, list)
		if len(lists) > domain.MaxECHKeys {
			return nil, domain.ErrInvalidOutput
		}
	}
	if len(lists) == 0 {
		return nil, domain.ErrInvalidOutput
	}
	return lists, nil
}
func (r *Runner) ECH(ctx context.Context, input domain.ECHInput) (domain.ECHResult, error) {
	args := []string{"tls", "ech"}
	if input.ServerName != nil {
		args = append(args, "--serverName="+*input.ServerName)
	}
	var restored []byte
	if input.ServerKeys != nil {
		restored, _ = base64.StdEncoding.DecodeString(*input.ServerKeys)
		defer clear(restored)
		if _, err := echLists(restored); err != nil {
			return domain.ECHResult{}, domain.ErrInvalidArgument
		}
		args = append(args, "-i", *input.ServerKeys)
	}
	if input.Format == domain.PEM {
		args = append(args, "--pem")
	}
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.ECHResult{}, err
	}
	defer release()
	raw, err := r.run(ctx, args...)
	if err != nil {
		return domain.ECHResult{}, err
	}
	defer clear(raw)
	var configs, keys []byte
	if input.Format == domain.PEM {
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "ECH CONFIGS" || len(block.Headers) != 0 {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
		configs = block.Bytes
		block, rest = pem.Decode(rest)
		if block == nil || block.Type != "ECH KEYS" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
		keys = block.Bytes
	} else {
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(lines) != 4 || lines[0] != "ECH config list: " || lines[2] != "ECH server keys: " {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
		configs, err = base64.StdEncoding.Strict().DecodeString(lines[1])
		if err != nil {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
		keys, err = base64.StdEncoding.Strict().DecodeString(lines[3])
		if err != nil {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
	}
	defer clear(keys)
	lists, err := echLists(keys)
	if err != nil {
		return domain.ECHResult{}, err
	}
	if !equal(bytes.Join(lists, nil), configs) || (input.ServerKeys != nil && !equal(restored, keys)) {
		return domain.ECHResult{}, domain.ErrInvalidOutput
	}
	if input.ServerKeys == nil {
		expected := "cloudflare-ech.com"
		if input.ServerName != nil {
			expected = *input.ServerName
		}
		// Native config carries this bounded public name verbatim.
		if !bytes.Contains(configs, []byte(expected)) {
			return domain.ECHResult{}, domain.ErrInvalidOutput
		}
	}
	result := domain.ECHResult{Format: input.Format, ConfigLists: make([]string, len(lists))}
	if input.Format == domain.PEM {
		result.ServerKeys = string(pem.EncodeToMemory(&pem.Block{Type: "ECH KEYS", Bytes: keys}))
		for i, list := range lists {
			result.ConfigLists[i] = string(pem.EncodeToMemory(&pem.Block{Type: "ECH CONFIGS", Bytes: list}))
		}
	} else {
		result.ServerKeys = base64.StdEncoding.EncodeToString(keys)
		for i, list := range lists {
			result.ConfigLists[i] = base64.StdEncoding.EncodeToString(list)
		}
	}
	return result, nil
}
