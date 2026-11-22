//go:build integration && linux

package integration_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeFamilyAccountAuthenticationAndRevocation(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, family := range nativeAccountFamilies() {
		t.Run(family.name, func(t *testing.T) {
			for _, transport := range []string{"http", "grpc", "nats"} {
				t.Run(transport, func(t *testing.T) {
					const email = "Managed@example.test"
					target := accountTarget(t)
					f := newFixture(t, pki, broker, transport)
					configureFamily(t, &f, family)
					address, virtualTarget := familyTrafficTarget(t, f, target.Listener.Addr().String())
					// Separate native clients prevent cached authenticated QUIC or
					// WireGuard sessions from masquerading as new authentication.
					before, beforeAPI := familyTrafficClient(t, &f, family, "before", address, email, 3)
					after, afterAPI := familyTrafficClient(t, &f, family, "after", address, email, 3)
					f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
					f.config.Communications.Requests.Timeout = 2 * time.Second
					if f.config.Communications.HTTP != nil {
						f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
					}
					c := startNode(t, f)
					for _, api := range []string{f.native, beforeAPI, afterAPI} {
						awaitNative(t, api)
					}
					result, code := sourceAccount(t, c, transport, "account.json", email, familySource(t, family, email, 3))
					if code != "" || !result.Acknowledged {
						t.Fatal(result, code)
					}
					if err := familyTraffic(t, before, virtualTarget); err != nil {
						t.Fatal("added native account could not reach the isolated target", err)
					}
					result, code = accountMutation(t, c, transport, "remove", email, nil)
					if code != "" || !result.Acknowledged {
						t.Fatal(result, code)
					}
					if familyTraffic(t, after, virtualTarget) == nil {
						t.Fatal("removed account authenticated from a fresh native client")
					}
					assertFamilyCount(t, c, transport, 1)
				})
			}
		})
	}
}

func familyTraffic(t *testing.T, proxy, target string) error {
	t.Helper()
	address, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(address), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32))
	if err != nil {
		return err
	}
	if response.StatusCode != 200 || string(data) != "pong" {
		return fmt.Errorf("native traffic status=%d body=%q", response.StatusCode, data)
	}
	return nil
}
