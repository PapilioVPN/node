package jsonwire_test

import (
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
)

func TestJSONRetainsIntegerPrecisionAndEmptyCollection(t *testing.T) {
	data, err := jsonwire.Encode(domain.Records{Generation: math.MaxUint64})
	if err != nil || !strings.Contains(string(data), `"generation":"18446744073709551615"`) || !strings.Contains(string(data), `"outbounds":[]`) {
		t.Fatal(string(data), err)
	}
}
