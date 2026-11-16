// Package grpcapi composes the user adapters for the existing XrayService.
package grpcapi

import (
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/observations"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/online"
)

// UserAPI promotes the focused adapters without forwarding RPCs or duplicating
// the single generated fallback required by the existing XrayService assembly.
type UserAPI struct {
	fallback
	mutations.MutationAPI
	observations.ObservationAPI
	online.OnlineAPI
}

// The extra embedding depth lets adapter methods take precedence over generated
// unimplemented methods while preserving the generated server safety contract.
type fallback struct {
	xrayv1.UnimplementedXrayServiceServer
}
