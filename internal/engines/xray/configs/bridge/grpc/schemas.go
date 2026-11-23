package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/configs.proto

import (
	"context"
	"errors"

	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func response(result validation.Result, err error) (*configsv1.XrayConfigValidation, error) {
	if err != nil {
		switch {
		case errors.Is(err, validation.ErrNotFound):
			return nil, status.Error(codes.NotFound, validation.ErrNotFound.Error())
		case errors.Is(err, validation.ErrBusy):
			return nil, status.Error(codes.Aborted, validation.ErrBusy.Error())
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, "xray validation canceled")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(codes.DeadlineExceeded, "xray validation deadline exceeded")
		default:
			return nil, status.Error(codes.Unavailable, validation.ErrFailed.Error())
		}
	}

	data := &configsv1.XrayConfigValidation{Id: result.ID, Valid: result.Issue == nil}
	if issue := result.Issue; issue != nil {
		code := configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_REJECTED
		if issue.Code == validation.DecodeError {
			code = configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR
		}
		data.Issue = &configsv1.XrayConfigValidationIssue{Code: code, Line: issue.Line, Column: issue.Column}
	}
	return data, nil
}
