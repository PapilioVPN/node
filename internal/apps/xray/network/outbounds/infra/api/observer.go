// Package api observes existing native Xray outbound probe results.
package api

import (
	"context"

	observatory "github.com/xtls/xray-core/app/observatory/command"
)

type API interface {
	Begin(context.Context) (context.Context, func(), error)
	ObservatoryClient() (observatory.ObservatoryServiceClient, error)
}

type Observer struct{ API API }
