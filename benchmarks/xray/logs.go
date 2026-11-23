package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	logsv1 "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	logEvents   = 384
	logInterval = 2 * time.Millisecond
	logSettle   = 50 * time.Millisecond
	logDrain    = 300 * time.Millisecond
)

type logWorkload struct{ proxy, raw string }

type logStream func(context.Context) (receive func() (string, error), close func(), err error)

func logsHTTP(client *http.Client, address string, reference bool) logStream {
	path := "/v1/xray/instances/primary/logs"
	if reference {
		path = "/logs"
	}
	return func(ctx context.Context) (func() (string, error), func(), error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+path, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("x-api-key", key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		closeBody := func() { _ = resp.Body.Close() }
		if resp.StatusCode != http.StatusOK {
			closeBody()
			return nil, nil, fmt.Errorf("GetLogs returned %s", resp.Status)
		}
		if reference {
			reader := bufio.NewReader(resp.Body)
			return func() (string, error) { return reader.ReadString('\n') }, closeBody, nil
		}
		decoder := json.NewDecoder(resp.Body)
		return func() (string, error) {
			var record struct{ Message []byte }
			err := decoder.Decode(&record)
			return string(record.Message), err
		}, closeBody, nil
	}
}

func logsGRPC(nodeConn, refConn *grpc.ClientConn) (logStream, logStream) {
	node := logsv1.NewXrayLogsServiceClient(nodeConn)
	nodeLogs := func(ctx context.Context) (func() (string, error), func(), error) {
		stream, err := node.GetLogs(metadata.AppendToOutgoingContext(ctx, "x-api-key", key),
			&logsv1.XrayLogsRequest{Id: "primary"})
		if err != nil {
			return nil, nil, err
		}
		return func() (string, error) {
			event, err := stream.Recv()
			return string(event.GetMessage()), err
		}, func() {}, nil
	}
	referenceLogs := func(ctx context.Context) (func() (string, error), func(), error) {
		stream, err := refConn.NewStream(metadata.AppendToOutgoingContext(ctx, "x-api-key", key),
			&grpc.StreamDesc{ServerStreams: true}, "/service.NodeService/GetLogs")
		if err != nil {
			return nil, nil, err
		}
		if err := stream.SendMsg(new(emptypb.Empty)); err != nil {
			return nil, nil, err
		}
		if err := stream.CloseSend(); err != nil {
			return nil, nil, err
		}
		return func() (string, error) {
			// The reference Log has one proto3 string at field 1, the same
			// wire layout as StringValue. Both clients use generated decoding.
			event := new(wrapperspb.StringValue)
			err := stream.RecvMsg(event)
			return event.GetValue(), err
		}, func() {}, nil
	}
	return nodeLogs, referenceLogs
}

type logSample struct {
	emitted  time.Time
	received time.Time
}

type logCapture struct {
	requested time.Time
	firstWire time.Time
	firstLive time.Time
	events    [logEvents]logSample
	err       error
}

func benchmarkLogs(protocol string, targets []target, rounds int, work logWorkload) {
	raw, err := os.Create(work.raw)
	check(err)
	compressed := gzip.NewWriter(raw)
	samples := csv.NewWriter(compressed)
	check(samples.Write([]string{"transport", "round", "node", "subscribers", "subscriber", "probe",
		"request_unix_ns", "first_wire_unix_ns", "probe_unix_ns", "xray_unix_ns", "received_unix_ns"}))
	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"transport", "round", "node", "subscribers", "sent", "received", "lost",
		"producer_ns", "events_per_second", "first_wire_p50_us", "first_live_p50_us", "delivery_p50_us", "delivery_p95_us"}))

	for round := range rounds {
		for _, count := range []int{1, 8} {
			order := []int{0, 1}
			if round%2 != 0 {
				slices.Reverse(order)
			}
			for _, index := range order {
				target := targets[index]
				prefix := fmt.Sprintf("logbench-%d-%d-", round+1, count)
				captures, sent, elapsed, err := captureLogs(target, work.proxy, prefix, count)
				if err != nil {
					check(fmt.Errorf("%s, %d subscribers: %w", target.name, count, err))
				}
				firstWire, firstLive, delays := []time.Duration{}, []time.Duration{}, []time.Duration{}
				for subscriber, capture := range captures {
					if !capture.firstWire.IsZero() {
						firstWire = append(firstWire, capture.firstWire.Sub(capture.requested))
					}
					if !capture.firstLive.IsZero() {
						firstLive = append(firstLive, capture.firstLive.Sub(capture.requested))
					}
					for index, event := range capture.events {
						if !event.received.IsZero() {
							delays = append(delays, event.received.Sub(event.emitted))
						}
						check(samples.Write([]string{protocol, strconv.Itoa(round + 1), target.name, strconv.Itoa(count),
							strconv.Itoa(subscriber + 1), strconv.Itoa(index + 1), unixNanos(capture.requested),
							unixNanos(capture.firstWire), unixNanos(sent[index]), unixNanos(event.emitted), unixNanos(event.received)}))
					}
				}
				slices.Sort(firstWire)
				slices.Sort(firstLive)
				slices.Sort(delays)
				check(output.Write([]string{protocol, strconv.Itoa(round + 1), target.name, strconv.Itoa(count),
					strconv.Itoa(count * logEvents), strconv.Itoa(len(delays)), strconv.Itoa(count*logEvents - len(delays)),
					strconv.FormatInt(elapsed.Nanoseconds(), 10), fmt.Sprintf("%.3f", float64(len(delays))/elapsed.Seconds()),
					logPercentile(firstWire, 50), logPercentile(firstLive, 50), logPercentile(delays, 50), logPercentile(delays, 95)}))
			}
		}
	}
	output.Flush()
	check(output.Error())
	samples.Flush()
	check(samples.Error())
	check(compressed.Close())
	check(raw.Close())
}

func captureLogs(target target, proxy, prefix string, count int) (captures []logCapture, sent [logEvents]time.Time, elapsed time.Duration, err error) {
	control, cancelControl := context.WithTimeout(context.Background(), 8*time.Second)
	err = target.call(control, start)
	cancelControl()
	if err != nil {
		return
	}
	defer func() {
		control, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if stopErr := target.call(control, stop); err == nil {
			err = stopErr
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	captures = make([]logCapture, count)
	started := make(chan struct{}, count)
	var readers sync.WaitGroup
	defer func() {
		cancel()
		readers.Wait()
	}()
	for i := range captures {
		readers.Go(func() {
			capture := &captures[i]
			capture.requested = time.Now()
			started <- struct{}{}
			receive, closeStream, err := target.logs(ctx)
			if err != nil {
				capture.err = err
				return
			}
			defer closeStream()
			capture.record(ctx, prefix, receive)
		})
	}
	for range count {
		<-started
	}
	time.Sleep(logSettle)
	begin := time.Now()
	for i := range sent {
		time.Sleep(time.Until(begin.Add(time.Duration(i) * logInterval)))
		sent[i] = time.Now()
		if err = logProbe(ctx, proxy, fmt.Sprintf("%s%04d.invalid:80", prefix, i+1)); err != nil {
			return
		}
	}
	elapsed = time.Since(begin)
	time.Sleep(logDrain)
	cancel()
	readers.Wait()
	for i := range captures {
		if err = captures[i].err; err != nil {
			return
		}
		if captures[i].firstLive.IsZero() {
			err = fmt.Errorf("subscriber %d received no matching access records", i+1)
			return
		}
	}
	return
}

func (capture *logCapture) record(ctx context.Context, prefix string, receive func() (string, error)) {
	timestamp := "2006/01/02 15:04:05.000000"
	for {
		line, err := receive()
		received := time.Now()
		if err != nil {
			if ctx.Err() == nil {
				capture.err = err
			}
			return
		}
		if capture.firstWire.IsZero() {
			capture.firstWire = received
		}
		_, dest, accepted := strings.Cut(line, " accepted ")
		_, match, found := strings.Cut(dest, prefix)
		if !accepted || !found {
			continue
		}
		id, _, found := strings.Cut(match, ".invalid:80")
		index, err := strconv.Atoi(id)
		if !found || err != nil || index < 1 || index > logEvents {
			capture.err = fmt.Errorf("unexpected Xray access marker %q", match)
			return
		}
		if len(line) < len(timestamp) {
			capture.err = fmt.Errorf("Xray access record is missing its timestamp")
			return
		}
		emitted, err := time.Parse(timestamp, line[:len(timestamp)])
		if err != nil {
			capture.err = err
			return
		}
		event := &capture.events[index-1]
		if !event.received.IsZero() {
			capture.err = fmt.Errorf("duplicate Xray access marker %d", index)
			return
		}
		*event = logSample{emitted, received}
		if capture.firstLive.IsZero() {
			capture.firstLive = received
		}
	}
}

func logProbe(ctx context.Context, address, host string) error {
	var dial net.Dialer
	conn, err := dial.DialContext(ctx, "tcp4", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host); err != nil {
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Xray CONNECT returned %s", resp.Status)
	}
	return nil
}

func unixNanos(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UnixNano(), 10)
}

func logPercentile(durations []time.Duration, rank int) string {
	if len(durations) == 0 {
		return ""
	}
	return percentile(durations, rank)
}
