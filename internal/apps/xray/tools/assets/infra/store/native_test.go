package store

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestGeodataValidatesAllEntries(t *testing.T) {
	ip, _ := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "TEST", Cidr: []*geodata.CIDR{{Ip: []byte{127, 0, 0, 0}, Prefix: 8}}}}})
	site, _ := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: "TEST", Domain: []*geodata.Domain{{Type: geodata.Domain_Full, Value: "example.test"}}}}})
	if info, err := geodataInfo(context.Background(), ip, site); err != nil || info.ipCount != 1 || info.siteCount != 1 {
		t.Fatal(info, err)
	}
	malformed, _ := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "TEST", Cidr: []*geodata.CIDR{{Ip: []byte{127, 0, 0, 0}, Prefix: 8}}}, {Code: "SECOND", Cidr: []*geodata.CIDR{{Ip: []byte{1, 2, 3, 4}, Prefix: 33}}}}})
	if _, err := geodataInfo(context.Background(), malformed, site); !errors.Is(err, domain.ErrRejected) {
		t.Fatal(err)
	}
	regex, _ := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: "TEST", Domain: []*geodata.Domain{{Type: geodata.Domain_Regex, Value: "["}}}}})
	if _, err := geodataInfo(context.Background(), ip, regex); !errors.Is(err, domain.ErrRejected) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := geodataInfo(ctx, ip, site); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func FuzzGeodata(f *testing.F) {
	f.Add([]byte{1, 2, 3}, []byte{2, 3})
	f.Fuzz(func(t *testing.T, ip, site []byte) {
		if len(ip)+len(site) > 1<<20 {
			t.Skip()
		}
		_, _ = geodataInfo(context.Background(), ip, site)
	})
}
