package native

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func ReadDefaultPeerKey(path string) ([32]byte, error) {
	var key [32]byte
	if path == "" {
		return key, nil
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return key, errors.New("cannot open wireguard.peer_preshared_key_file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128 {
		return key, errors.New("wireguard.peer_preshared_key_file must be a regular file of at most 128 bytes")
	}
	data, err := io.ReadAll(io.LimitReader(file, 129))
	defer clear(data)
	if err != nil || len(data) > 128 {
		return key, errors.New("cannot read wireguard.peer_preshared_key_file")
	}
	value := bytes.TrimSpace(data)
	if len(value) != base64.StdEncoding.EncodedLen(len(key)) {
		return key, errors.New("wireguard.peer_preshared_key_file must contain a base64 32-byte key")
	}
	count, err := base64.StdEncoding.Strict().Decode(key[:], value)
	if err != nil || count != len(key) || key == [32]byte{} {
		return [32]byte{}, errors.New("wireguard.peer_preshared_key_file must contain a base64 nonzero 32-byte key")
	}
	return key, nil
}
