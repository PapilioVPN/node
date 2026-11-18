package services_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

type logindFixture struct {
	calls     int
	selection domain.Selection
	cause     error
}

func (reader *logindFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	return domain.Page{Sessions: []domain.Summary{{ID: "s1", UID: 0, User: "test"}}}, reader.cause
}
func (reader *logindFixture) Read(_ context.Context, id string) (domain.Details, error) {
	reader.calls++
	return domain.Details{Summary: domain.Summary{ID: id}}, reader.cause
}

type recordsFixture struct {
	calls int
	cause error
}

func (reader *recordsFixture) List(context.Context, domain.Selection) (domain.RecordPage, error) {
	reader.calls++
	return domain.RecordPage{Records: []domain.LoginRecord{{RecordID: "p1", User: "test"}}}, reader.cause
}
func TestSessionSelectionsAndNativeFailures(t *testing.T) {
	logind := &logindFixture{}
	records := &recordsFixture{}
	service := services.Service{Logind: logind, Records: records}
	for _, selection := range []domain.Selection{{Limit: 257}, {Offset: 16385}} {
		if _, err := service.List(context.Background(), selection); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
		if _, err := service.ListRecords(context.Background(), selection); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 256), "a\x00b", string([]byte{255})} {
		if _, err := service.Read(context.Background(), id); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if logind.calls != 0 || records.calls != 0 {
		t.Fatal("invalid input reached native I/O")
	}
	page, err := service.List(context.Background(), domain.Selection{})
	if err != nil || logind.selection.Limit != 128 || len(page.Page.Sessions) != 1 || page.ObservedAt.IsZero() {
		t.Fatalf("%+v %v", page, err)
	}
	read, err := service.Read(context.Background(), "s1")
	if err != nil || read.Details.Summary.ID != "s1" {
		t.Fatalf("%+v %v", read, err)
	}
	legacy, err := service.ListRecords(context.Background(), domain.Selection{})
	if err != nil || len(legacy.Page.Records) != 1 {
		t.Fatalf("%+v %v", legacy, err)
	}
	cause := errors.New("native cause")
	logind.cause = cause
	records.cause = cause
	if _, err := service.List(context.Background(), domain.Selection{}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := service.Read(context.Background(), "s1"); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := service.ListRecords(context.Background(), domain.Selection{}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
