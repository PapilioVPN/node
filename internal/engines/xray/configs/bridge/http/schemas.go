package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
)

type validationIssue struct {
	Code   validation.IssueCode `json:"code"`
	Line   uint32               `json:"line,omitempty"`
	Column uint32               `json:"column,omitempty"`
}

func writeResult(w http.ResponseWriter, result validation.Result, err error) {
	if err != nil {
		message, code := validation.ErrFailed.Error(), http.StatusServiceUnavailable
		switch {
		case errors.Is(err, validation.ErrNotFound):
			message, code = validation.ErrNotFound.Error(), http.StatusNotFound
		case errors.Is(err, validation.ErrBusy):
			message, code = validation.ErrBusy.Error(), http.StatusConflict
		case errors.Is(err, runconfig.ErrSourceTooLarge):
			message, code = runconfig.ErrSourceTooLarge.Error(), http.StatusRequestEntityTooLarge
		case errors.Is(err, context.Canceled):
			message, code = "xray validation canceled", http.StatusRequestTimeout
		case errors.Is(err, context.DeadlineExceeded):
			message, code = "xray validation deadline exceeded", http.StatusGatewayTimeout
		}
		http.Error(w, message, code)
		return
	}

	data := struct {
		ID    string           `json:"id"`
		Valid bool             `json:"valid"`
		Issue *validationIssue `json:"issue,omitempty"`
	}{ID: result.ID, Valid: result.Issue == nil}
	if issue := result.Issue; issue != nil {
		data.Issue = &validationIssue{Code: issue.Code, Line: issue.Line, Column: issue.Column}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}
