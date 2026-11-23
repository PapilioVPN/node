package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
)

type streamFrame struct {
	Index    *uint32        `json:"index"`
	Last     bool           `json:"last"`
	Accounts []accountInput `json:"accounts"`
}

func syncUsersChunked(service *membership.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), membership.StreamTimeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(deadline); err != nil {
			writeError(w, membership.ErrUnavailable)
			return
		}
		defer controller.SetReadDeadline(time.Time{})
		body := http.MaxBytesReader(w, r.Body, 2<<20)
		defer body.Close()
		scanner := bufio.NewScanner(body)
		scanner.Buffer(nil, 512<<10)
		inbound := scope(r)
		result, err := service.SyncUsersChunked(ctx, func() (membership.UsersChunk, error) {
			return readFrame(scanner, inbound)
		})
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Xray account request is too large", http.StatusRequestEntityTooLarge)
			return
		}
		writeSync(w, result, err)
	}
}

func readFrame(scanner *bufio.Scanner, inbound membership.Scope) (membership.UsersChunk, error) {
	if !scanner.Scan() {
		err := scanner.Err()
		var tooLarge *http.MaxBytesError
		switch {
		case err == nil:
			return membership.UsersChunk{}, io.EOF
		case errors.As(err, &tooLarge):
			return membership.UsersChunk{}, err
		case errors.Is(err, os.ErrDeadlineExceeded):
			return membership.UsersChunk{}, context.DeadlineExceeded
		default:
			return membership.UsersChunk{}, membership.ErrInvalid
		}
	}
	var input streamFrame
	decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Index == nil || input.Accounts == nil ||
		len(input.Accounts) > membership.MaxChunkAccounts {
		return membership.UsersChunk{}, membership.ErrInvalid
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return membership.UsersChunk{}, membership.ErrInvalid
	}
	accounts, err := decodeAccounts(input.Accounts)
	return membership.UsersChunk{Scope: inbound, Index: *input.Index, Last: input.Last, Accounts: accounts}, err
}
