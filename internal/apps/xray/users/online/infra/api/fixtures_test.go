//go:build integration

package api_test

import (
	"testing"
	"time"

	trafficapi "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/infra/api"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	onlineapi "github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
)

// The observation adapter verifies shared capacity and channel ownership.
type apiClientSet struct {
	*nativeapi.Client
	*onlineapi.Online
	*trafficapi.Counters
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
	return &apiClientSet{Client: client, Online: &onlineapi.Online{API: client}, Counters: &trafficapi.Counters{API: client}, Reader: &infoapi.Reader{API: client}}
}
