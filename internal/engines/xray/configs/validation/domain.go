package validation

import "errors"

var (
	ErrNotFound = errors.New("xray instance not found")
	ErrBusy     = errors.New("xray configuration validation is already running")
	ErrFailed   = errors.New("xray configuration validation could not finish")
)

type IssueCode string

const (
	DecodeError    IssueCode = "config_decode_error"
	ConfigRejected IssueCode = "config_rejected"
)

type Issue struct {
	Code   IssueCode
	Line   uint32
	Column uint32
}

type Result struct {
	ID    string
	Issue *Issue
}
