//go:build linux && amd64

package utmp

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

func loginRecord() []byte {
	data := make([]byte, 384)
	binary.LittleEndian.PutUint16(data, 7)
	binary.LittleEndian.PutUint32(data[4:8], 42)
	copy(data[8:40], "pts/1")
	copy(data[40:44], "p1")
	copy(data[44:76], "test")
	copy(data[76:332], "example.test")
	binary.LittleEndian.PutUint32(data[336:340], 17)
	binary.LittleEndian.PutUint32(data[340:344], 123)
	binary.LittleEndian.PutUint32(data[344:348], 456789)
	return data
}
func TestLoginABIProjectionOrderingAndBounds(t *testing.T) {
	record := loginRecord()
	data := append(append(make([]byte, 384), record...), record...)
	page, err := parse(context.Background(), data, domain.Selection{Limit: 1})
	if err != nil || len(page.Records) != 1 || page.NextOffset == nil || *page.NextOffset != 1 || page.Records[0].PID != 42 || page.Records[0].RecordID != "p1" || page.Records[0].User != "test" || page.Records[0].Terminal != "pts/1" || page.Records[0].Host != "example.test" || page.Records[0].SessionID != 17 || page.Records[0].StartedUnixUS != 123456789 {
		t.Fatalf("%+v %v", page, err)
	}
	last, err := parse(context.Background(), data, domain.Selection{Offset: 1, Limit: 1})
	if err != nil || len(last.Records) != 1 || last.Records[0] != page.Records[0] || last.NextOffset != nil {
		t.Fatalf("duplicates/order lost: %+v %v", last, err)
	}
	for _, input := range [][]byte{record[:383], append(record, 0)} {
		if _, err := parse(context.Background(), input, domain.Selection{Limit: 1}); err == nil {
			t.Fatal("ignored truncated record")
		}
	}
	if _, err := parse(context.Background(), make([]byte, maxBytes+1), domain.Selection{Limit: 1}); !errors.Is(err, domain.ErrResourceExhausted) {
		t.Fatal(err)
	}
	bad := loginRecord()
	binary.LittleEndian.PutUint32(bad[344:348], 1000000)
	if _, err := parse(context.Background(), bad, domain.Selection{Limit: 1}); err == nil {
		t.Fatal("accepted invalid timestamp")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parse(ctx, record, domain.Selection{Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestLoginFileAndMissingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utmp")
	if err := os.WriteFile(path, loginRecord(), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := (Reader{Path: path}).List(context.Background(), domain.Selection{Limit: 1})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := (Reader{Path: path + "-missing"}).List(context.Background(), domain.Selection{Limit: 1}); !errors.Is(err, domain.ErrUnavailable) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func FuzzLoginRecords(f *testing.F) {
	f.Add(loginRecord())
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		page, err := parse(context.Background(), data, domain.Selection{Limit: 128})
		if err == nil && len(page.Records) > 128 {
			t.Fatal("unbounded page")
		}
	})
}
