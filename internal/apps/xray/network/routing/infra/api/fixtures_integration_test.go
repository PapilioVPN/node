//go:build integration

package api_test

import (
	"testing"
	"time"

	routingapi "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
)

// The observation adapter verifies shared capacity and channel ownership.
type apiClientSet struct {
	*nativeapi.Client
	*routingapi.Router
	*infoapi.Reader
}

func apiClient(t *testing.T, address string, timeout time.Duration, limit int) *apiClientSet {
	t.Helper()
	client, err := nativeapi.NewClient(nativeapi.Config{Network: "tcp", Address: address, RequestTimeout: timeout, MaxReceiveBytes: limit, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return &apiClientSet{Client: client, Router: &routingapi.Router{API: client}, Reader: &infoapi.Reader{API: client}}
}
