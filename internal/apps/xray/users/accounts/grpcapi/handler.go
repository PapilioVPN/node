// Package grpcapi composes the user adapters for the existing XrayService.
package grpcapi

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/observations"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
)

// UserAPI promotes the focused adapters without forwarding RPCs or duplicating
// the single generated fallback required by the existing XrayService assembly.
type UserAPI struct {
	fallback
	mutations.MutationAPI
	observations.ObservationAPI
}

// The extra embedding depth lets adapter methods take precedence over generated
// unimplemented methods while preserving the generated server safety contract.
type fallback struct {
	xrayv1.UnimplementedXrayServiceServer
}
