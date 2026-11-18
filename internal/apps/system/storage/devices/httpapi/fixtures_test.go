package httpapi_test

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
)

type readerFixture struct {
	calls     int
	cause     error
	selection domain.Selection
}

func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	cursor := "sdb"
	return domain.Page{Devices: []domain.Device{{Major: 8, Name: "sda", ReadSectors: 9007199254740993, ReadMilliseconds: 4, InProgress: 9}, {Major: 8, Minor: 16, Name: "sdb", WriteSectors: 18446744073709551615, Discard: &domain.Discard{}, Flush: &domain.Flush{Milliseconds: 17}}}, NextAfterName: &cursor}, reader.cause
}
