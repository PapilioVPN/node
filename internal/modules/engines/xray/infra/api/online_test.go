package api

import (
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func FuzzOnlineUserEmail(f *testing.F) {
	for _, key := range []string{"", "user>>>>>>online", "user>>>reader>>>online", "user>>> Mixed>>>@Example >>>online", "user>>>   >>>online", "other>>>user>>>reader>>>online", "user>>>reader>>>online-extra"} {
		f.Add(key)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if len(key) > 4096 {
			t.Skip()
		}
		email, err := onlineUserEmail(key)
		if err != nil {
			if !errors.Is(err, domain.ErrInvalidOnlineResponse) || email != "" {
				t.Fatalf("failed decode leaked an identity: %q, %v", email, err)
			}
			return
		}
		if email == "" || "user>>>"+string(email)+">>>online" != key {
			t.Fatalf("native identity failed round trip: %q => %q", key, email)
		}
	})
}
