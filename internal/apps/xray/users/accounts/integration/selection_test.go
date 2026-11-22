//go:build integration && linux

package integration_test

import (
	"context"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

func TestNativeSourceSelectionSupportsEveryAccountFamily(t *testing.T) {
	compiler := accountCompiler(t)
	p := configuration.Preparer{Compiler: compiler}
	for _, test := range nativeAccountFamilies() {
		t.Run(test.name, func(t *testing.T) {
			source := familySource(t, test, "selected@example.test", 2)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			raw, err := p.Prepare(ctx, "a", "members", "selected@example.test", []configs.Source{{Name: "account.json", Content: source}})
			if err != nil {
				t.Fatal(err)
			}
			var user protocol.User
			if err := proto.Unmarshal(raw, &user); err != nil || user.Email != "selected@example.test" || user.Level != 7 || user.Account == nil || user.Account.Type != test.account {
				t.Fatal(&user, err)
			}
		})
	}
}
