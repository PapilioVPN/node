//go:build linux

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

// Native decoding checks every entry, rather than merely checking file presence
// or one country. Protobuf byte limits do not bound native/protobuf heap usage.
func geodataInfo(ctx context.Context, ip, site []byte) (uint32, uint32, error) {
	var ips geodata.GeoIPList
	var sites geodata.GeoSiteList
	if proto.Unmarshal(ip, &ips) != nil || proto.Unmarshal(site, &sites) != nil || len(ips.Entry) == 0 || len(sites.Entry) == 0 {
		return 0, 0, domain.ErrRejected
	}
	codes := map[string]bool{}
	for _, e := range ips.Entry {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		key := strings.ToUpper(e.Code)
		if !validCode(key) || codes[key] || len(e.Cidr) == 0 {
			return 0, 0, domain.ErrRejected
		}
		codes[key] = true
		for _, c := range e.Cidr {
			if c == nil || (len(c.Ip) != 4 && len(c.Ip) != 16) || c.Prefix > uint32(8*len(c.Ip)) {
				return 0, 0, domain.ErrRejected
			}
		}
	}
	codes = map[string]bool{}
	for _, e := range sites.Entry {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		key := strings.ToUpper(e.Code)
		if !validCode(key) || codes[key] || len(e.Domain) == 0 {
			return 0, 0, domain.ErrRejected
		}
		codes[key] = true
		for _, d := range e.Domain {
			if d == nil || d.Value == "" || d.Type < geodata.Domain_Substr || d.Type > geodata.Domain_Full {
				return 0, 0, domain.ErrRejected
			}
			if d.Type == geodata.Domain_Regex {
				if _, err := regexp.Compile(d.Value); err != nil {
					return 0, 0, domain.ErrRejected
				}
			}
		}
	}
	if len(ips.Entry) > 65536 || len(sites.Entry) > 65536 {
		return 0, 0, domain.ErrCapacity
	}
	return uint32(len(ips.Entry)), uint32(len(sites.Entry)), nil
}
func validCode(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (o *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > 4096-o.Len() {
		o.overflow = true
		return 0, domain.ErrCapacity
	}
	return o.Buffer.Write(p)
}
func version(ctx context.Context, binary string) (string, error) {
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, binary, "version")
	cmd.Env = []string{}
	cmd.Dir = filepath.Dir(binary)
	cmd.WaitDelay = 100 * time.Millisecond
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if child.Err() != nil {
		return "", child.Err()
	}
	if out.overflow {
		return "", domain.ErrCapacity
	}
	if err != nil {
		return "", errors.Join(domain.ErrRejected, err)
	}
	line := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.HasPrefix(line, "Xray ") || len(line) > 512 {
		return "", domain.ErrRejected
	}
	return strings.TrimSpace(line), nil
}

// Probe both staged files using the candidate's native converter. All entry
// structure is checked above; conversion is not runtime construction/readiness.
func nativeCheck(ctx context.Context, dir string, ip, site []byte) error {
	var ips geodata.GeoIPList
	var sites geodata.GeoSiteList
	_ = proto.Unmarshal(ip, &ips)
	_ = proto.Unmarshal(site, &sites)
	data, err := json.Marshal(map[string]any{"routing": map[string]any{"rules": []any{
		map[string]any{"type": "field", "ip": []string{"geoip:" + ips.Entry[0].Code}, "outboundTag": "direct"},
		map[string]any{"type": "field", "domain": []string{"geosite:" + sites.Entry[0].Code}, "outboundTag": "direct"},
	}}, "outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}}})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "validation.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	defer os.Remove(path)
	child, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, filepath.Join(dir, "xray"), "convert", "pb", "-o", "/dev/null", path)
	cmd.Env = []string{"XRAY_LOCATION_ASSET=" + dir}
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	err = cmd.Run()
	if child.Err() != nil {
		return child.Err()
	}
	if err != nil {
		return errors.Join(domain.ErrRejected, err)
	}
	return nil
}
