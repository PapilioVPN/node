package cpu

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"sync/atomic"
	"time"
)

const cpuSampleInterval = time.Second

// Sampler publishes one immutable sample shared by all Host transports.
// Start and Stop are called once in order by the Host lifecycle.
type Sampler struct {
	latest      atomic.Pointer[Sample]
	cancel      context.CancelFunc
	done        chan struct{}
	logicalCPUs uint32
}

func NewSampler() *Sampler {
	return &Sampler{logicalCPUs: readLogicalCPUs()}
}

func (sampler *Sampler) LogicalCPUs() uint32 { return sampler.logicalCPUs }

func readLogicalCPUs() uint32 {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	if !scan.Scan() {
		return 0
	}
	var count uint32
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) < 4 || !bytes.HasPrefix(line, []byte("cpu")) || line[3] < '0' || line[3] > '9' {
			return count
		}
		count++
	}
	if scan.Err() != nil {
		return 0
	}
	return count
}

func (sampler *Sampler) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The Fx startup context expires independently of the application lifetime.
	runCtx, cancel := context.WithCancel(context.Background())
	sampler.cancel = cancel
	sampler.done = make(chan struct{})
	go sampler.run(runCtx)
	return nil
}

func (sampler *Sampler) Stop(ctx context.Context) error {
	sampler.cancel()
	select {
	case <-sampler.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sampler *Sampler) Latest() (Sample, bool) {
	sample := sampler.latest.Load()
	if sample == nil {
		return Sample{}, false
	}
	return *sample, true
}

func (sampler *Sampler) run(ctx context.Context) {
	defer close(sampler.done)
	defer sampler.latest.Store(nil)

	previous, err := readCPU()
	hasPrevious := err == nil
	ticker := time.NewTicker(cpuSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := readCPU()
			var sample *Sample
			if err == nil && hasPrevious {
				sample = cpuDelta(previous, current)
			}
			sampler.latest.Store(sample)
			previous, hasPrevious = current, err == nil
		}
	}
}

func readCPU() (cpuReading, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return cpuReading{}, err
	}
	defer file.Close()
	// Only the first line is needed, regardless of the number of CPUs.
	line, err := bufio.NewReaderSize(file, 512).ReadSlice('\n')
	if err != nil {
		return cpuReading{}, err
	}
	return parseCPU(line, time.Now())
}
