package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	bridge "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	httpstats "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	stats "github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type localRead struct{ context.Context }

func (localRead) Deadline() (time.Time, bool) { return time.Time{}, false }

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: components /path/to/xray")
	}
	dir, err := os.MkdirTemp("", "xray-stats-layers-")
	check(err)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "xray.json")
	check(os.WriteFile(path, []byte(`{"log":{"loglevel":"none"}}`), 0600))

	owner := lifecycle.New(lifecycle.Config{ID: "primary", Executable: os.Args[1], ConfigFile: path, RuntimeDirectory: dir})
	defer shutDown(owner.Close)
	ctx := context.Background()
	_, err = owner.Start(ctx, "primary")
	check(err)
	conn, err := owner.NativeConnection("primary")
	check(err)
	service := stats.New(owner)
	defer shutDown(service.Close)

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return bridge.RequireAPIKey("measure-runtime-stats", next) })
	httpstats.Register(router, service)
	httpRequest := httptest.NewRequest(http.MethodGet, "/v1/xray/instances/primary/stats/runtime", nil)
	httpRequest.Header.Set("x-api-key", "measure-runtime-stats")
	nativeRequest := new(emptypb.Empty)
	opts := []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024)}

	paths := []struct {
		name string
		read func()
	}{
		{"native", func() {
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			var result native.SysStatsResponse
			check(conn.Invoke(localRead{ctx}, native.GetSysStats, nativeRequest, &result, opts...))
			if result.GetNumGoroutine() == 0 {
				log.Fatal("missing native statistics")
			}
		}},
		{"service", func() {
			result, err := service.GetRuntimeStats(ctx, "primary")
			check(err)
			if result.Goroutines == 0 {
				log.Fatal("missing service statistics")
			}
		}},
		{"http_handler", func() {
			writer := httptest.NewRecorder()
			router.ServeHTTP(writer, httpRequest)
			if writer.Code != http.StatusOK {
				log.Fatalf("HTTP handler returned %d", writer.Code)
			}
		}},
	}
	time.Sleep(time.Second)
	for _, path := range paths {
		for range 100 {
			path.read()
		}
	}

	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"round", "path", "reads", "p50_us", "p95_us"}))
	const samples = 250
	for round := range 15 {
		for position := range paths {
			path := paths[(round+position)%len(paths)]
			timings := make([]time.Duration, samples)
			for n := range timings {
				start := time.Now()
				path.read()
				timings[n] = time.Since(start)
			}
			slices.Sort(timings)
			check(output.Write([]string{strconv.Itoa(round + 1), path.name, strconv.Itoa(samples),
				fmt.Sprintf("%.3f", float64(timings[samples/2])/1000),
				fmt.Sprintf("%.3f", float64(timings[(samples*95+99)/100-1])/1000)}))
		}
	}
	output.Flush()
	check(output.Error())
}

func shutDown(stop func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	check(stop(ctx))
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
