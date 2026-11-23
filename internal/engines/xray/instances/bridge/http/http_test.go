package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

func TestInstanceRoutesRejectInvalidRequests(t *testing.T) {
	router := chi.NewRouter()
	Register(router, lifecycle.New(lifecycle.Config{ID: "primary"}))
	for _, test := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/v1/xray/instances/missing", "", http.StatusNotFound},
		{http.MethodPut, "/v1/xray/instances/primary/execution", "{}", http.StatusBadRequest},
		{http.MethodDelete, "/v1/xray/instances/missing/execution", "", http.StatusNotFound},
		{http.MethodPost, "/v1/xray/instances/primary/execution", "", http.StatusMethodNotAllowed},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if recorder.Code != test.want {
			t.Errorf("%s %s = %d, want %d", test.method, test.path, recorder.Code, test.want)
		}
	}
}
