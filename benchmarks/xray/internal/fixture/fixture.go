package fixture

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative traffic_reference.proto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const Key = "8bc4e708-8b28-4705-8f3e-0bf78145e229"

// Action is the lifecycle step a workload requests from its
type Action int

const (
	Start Action = iota
	Status
	Stop
)

type LifecycleCall func(context.Context, Action) error

type Instance struct {
	Name string
	PID  string
	Call LifecycleCall
}

type Target[C any] struct {
	Instance
	Calls C
}

func WithCalls[C any](instances [2]Instance, calls [2]C) [2]Target[C] {
	return [2]Target[C]{{instances[0], calls[0]}, {instances[1], calls[1]}}
}

type ReadCall func(context.Context) (uint32, error)

func HasChild(pid string) bool { return len(ChildPIDs(pid)) != 0 }

func ChildPIDs(pid string) []string {
	paths, err := filepath.Glob("/proc/" + pid + "/task/*/children")
	Check(err)
	if len(paths) == 0 {
		log.Fatalf("cannot observe server %s children", pid)
	}
	var children []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		Check(err)
		children = append(children, strings.Fields(string(data))...)
	}
	return children
}

func Percentile(sorted []time.Duration, p int) string {
	return strconv.FormatFloat(float64(sorted[(len(sorted)*p+99)/100-1])/float64(time.Microsecond), 'f', 2, 64)
}

func Read(path string) []byte {
	data, err := os.ReadFile(path)
	Check(err)
	return data
}

func Check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

var ErrAtCapacity = errors.New("statistics read capacity reached")

func ParseClients(raw string) ([]int, error) {
	values := strings.Split(raw, ",")
	counts := make([]int, 0, len(values))
	for _, value := range values {
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || count < 1 || count > 256 {
			return nil, fmt.Errorf("runtime statistics client count must be between 1 and 256: %q", value)
		}
		counts = append(counts, count)
	}
	return counts, nil
}

type PollResult struct {
	Timings  []time.Duration
	Rejected int
	Elapsed  time.Duration
}

func Poll(ctx context.Context, call func(context.Context) (uint32, error), clients int, window time.Duration) (PollResult, error) {
	var ready, done sync.WaitGroup
	ready.Add(clients)
	started := make(chan struct{})
	timings := make([][]time.Duration, clients)
	rejections := make([]int, clients)
	failures := make([]error, clients)
	var deadline time.Time
	for i := range clients {
		done.Go(func() {
			_, err := call(ctx)
			if errors.Is(err, ErrAtCapacity) {
				err = nil
			}
			ready.Done()
			<-started
			for time.Now().Before(deadline) && err == nil {
				begin := time.Now()
				_, err = call(ctx)
				switch {
				case errors.Is(err, ErrAtCapacity):
					rejections[i]++
					err = nil
				case err == nil:
					timings[i] = append(timings[i], time.Since(begin))
				}
			}
			failures[i] = err
		})
	}
	ready.Wait()
	begin := time.Now()
	deadline = begin.Add(window)
	close(started)
	done.Wait()
	elapsed := time.Since(begin)

	if err := errors.Join(failures...); err != nil {
		return PollResult{}, err
	}
	all := slices.Concat(timings...)
	if len(all) == 0 {
		return PollResult{}, errors.New("no successful runtime statistics observations")
	}
	slices.Sort(all)
	rejected := 0
	for _, count := range rejections {
		rejected += count
	}
	return PollResult{Timings: all, Rejected: rejected, Elapsed: elapsed}, nil
}
func HTTPRead(ctx context.Context, client *http.Client, endpoint string, payload []byte) ([]byte, error) {
	return HTTPRequest(ctx, client, http.MethodGet, endpoint, payload)
}

func HTTPRequest(ctx context.Context, client *http.Client, method, endpoint string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", Key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusTooManyRequests:
		return nil, ErrAtCapacity
	default:
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, body)
	}
}

func CallError(err error) error {
	if grpcstatus.Code(err) == codes.ResourceExhausted {
		return ErrAtCapacity
	}
	return err
}
