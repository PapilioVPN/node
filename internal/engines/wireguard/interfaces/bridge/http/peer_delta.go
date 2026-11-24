package http

import (
	"encoding/base64"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/go-chi/chi/v5"
)

func registerPeerDelta(router chi.Router, path string, managed *lifecycle.Service) {
	router.Put(path+"/{key}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key, err := peerKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		var body *struct {
			PresharedKey []byte   `json:"preshared_key"`
			AllowedIPs   []string `json:"allowed_ips"`
		}
		if err := readPeerBody(w, r, &body); err != nil {
			writeError(w, err)
			return
		}
		if body == nil {
			writeError(w, peers.ErrInvalid)
			return
		}
		result, err := managed.UpsertPeer(r.Context(), interfaceName(r), peers.Input{
			PublicKey: key, PresharedKey: body.PresharedKey, AllowedIPs: body.AllowedIPs,
		})
		writePeerDelta(w, result, err)
	})
	router.Delete(path+"/{key}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		key, err := peerKey(r)
		if err == nil {
			err = managed.RemovePeer(r.Context(), interfaceName(r), key)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	router.Patch(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input peers.DeltaInput
		if err := readPeerBody(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		result, err := managed.ApplyPeerDelta(r.Context(), interfaceName(r), input)
		writePeerDelta(w, result, err)
	})
}

func peerKey(r *http.Request) ([]byte, error) {
	value := chi.URLParam(r, "key")
	if len(value) != base64.RawURLEncoding.EncodedLen(32) {
		return nil, peers.ErrInvalid
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, peers.ErrInvalid
	}
	return key, nil
}

func readPeerBody(w http.ResponseWriter, r *http.Request, out any) error {
	source, err := readConfig(w, r)
	if err != nil {
		return err
	}
	if err := jsonv2.Unmarshal(source, out, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(true)); err != nil {
		return peers.ErrInvalid
	}
	return nil
}

func writePeerDelta(w http.ResponseWriter, result peers.Result, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
