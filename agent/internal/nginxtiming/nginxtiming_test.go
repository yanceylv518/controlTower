package nginxtiming

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseLineVariants(t *testing.T) {
	line := `1.2.3.4 - "POST /v1/chat/completions?token=secret HTTP/1.1" [13/Jul/2026:10:00:00 +0800] status=504 rt=12.345 uct=0.001, 0.002 uht=1.234, 0.500 urt=12.344, 1.000 bytes=45678 req_len=890`
	entry, ok := ParseLine(line)
	if !ok || entry.Path != "/v1/chat/completions" || entry.Method != "POST" || entry.Status != 504 {
		t.Fatalf("unexpected parse: %#v ok=%v", entry, ok)
	}
	if entry.UCT != .003 || entry.UHT != 1.734 || entry.URT != 13.344 || !entry.HasUpstream {
		t.Fatalf("multi value sum failed: %#v", entry)
	}
	entry, ok = ParseLine(`x "GET /health HTTP/1.1" [13/Jul/2026:10:00:00 +0800] status=200 rt=0.1 uct=- uht=- urt=- bytes=2`)
	if !ok || entry.HasUpstream {
		t.Fatalf("dash values: %#v", entry)
	}
	if _, ok = ParseLine(`ordinary access log`); ok {
		t.Fatal("non-timed line accepted")
	}
}

func TestAggregatorStatsClassificationAndSamples(t *testing.T) {
	a := NewAggregator(10)
	base := time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC)
	entries := []Entry{
		{OccurredAt: base, Path: "/fast", Status: 200, RT: 1, UHT: .2, URT: .8, HasUpstream: true},
		{OccurredAt: base.Add(time.Second), Path: "/ttft", Status: 500, RT: 12, UHT: 8, URT: 11, HasUpstream: true},
		{OccurredAt: base.Add(2 * time.Second), Path: "/transfer", Status: 504, RT: 20, UHT: 2, URT: 19, HasUpstream: true},
	}
	for _, entry := range entries {
		a.Add(entry)
	}
	a.Flush(base.Add(time.Minute + bucketCloseGrace))
	buckets, samples := a.Snapshot()
	if len(buckets) != 1 || len(samples) != 2 {
		t.Fatalf("buckets=%d samples=%d", len(buckets), len(samples))
	}
	b := buckets[0]
	if b.RequestCount != 3 || b.Status5xx != 1 || b.Status504 != 1 || b.SlowTTFTCount != 1 || b.SlowTransferCount != 1 {
		t.Fatalf("classification: %#v", b)
	}
	if b.RTP50 != 12 || b.RTP95 != 20 || samples[0].Path != "/transfer" {
		t.Fatalf("stats/samples: %#v %#v", b, samples)
	}
	a.Ack(1)
	if pending, _ := a.Snapshot(); len(pending) != 0 {
		t.Fatal("ack did not remove bucket")
	}
}

func TestAggregatorMinuteBoundaryOutOfOrder(t *testing.T) {
	a := NewAggregator(10)
	base := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{base.Add(59 * time.Second), base.Add(time.Minute), base.Add(59 * time.Second)} {
		a.Add(Entry{OccurredAt: at, Path: "/x", Status: 200, RT: 1})
	}
	a.Flush(base.Add(2*time.Minute + bucketCloseGrace))
	buckets, _ := a.Snapshot()
	if len(buckets) != 2 || !buckets[0].BucketAt.Equal(base) || buckets[0].RequestCount != 2 {
		t.Fatalf("unexpected buckets: %#v", buckets)
	}
	// A late line for an already closed minute must not create a second bucket.
	a.Add(Entry{OccurredAt: base.Add(30 * time.Second), Path: "/late", Status: 200, RT: 1})
	a.Flush(base.Add(3 * time.Minute))
	buckets, _ = a.Snapshot()
	if len(buckets) != 2 {
		t.Fatalf("closed minute enqueued again: %#v", buckets)
	}
}

func TestAggregatorKeepsFiveSlowSamplesAndDropsOldestQueue(t *testing.T) {
	a := NewAggregator(1)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	for minute := 0; minute < maxQueuedBuckets+1; minute++ {
		for i := 0; i < 7; i++ {
			a.Add(Entry{OccurredAt: base.Add(time.Duration(minute) * time.Minute), Path: "/x", Status: 200, RT: float64(i + 1)})
		}
	}
	a.Flush(base.Add((maxQueuedBuckets + 2) * time.Minute))
	buckets, samples := a.Snapshot()
	if len(buckets) != maxQueuedBuckets {
		t.Fatalf("queue=%d", len(buckets))
	}
	if len(samples) != maxQueuedBuckets*maxSamplesPerBucket {
		t.Fatalf("samples=%d", len(samples))
	}
	if !buckets[0].BucketAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("oldest not dropped: %v", buckets[0].BucketAt)
	}
}

func TestTailerAppendRotationAndMissingFileRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timing.log")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewAggregator(10)
	baseAt := time.Now().UTC().Truncate(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Tailer{Path: path, Aggregator: a, Retry: 10 * time.Millisecond}.Run(ctx)
	time.Sleep(30 * time.Millisecond)
	appendLine(t, path, timedLine("/one", nginxTime(baseAt)))
	waitForBuckets(t, a, 1)
	if runtime.GOOS == "windows" {
		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		appendLine(t, path, timedLine("/two", nginxTime(baseAt.Add(time.Minute))))
	} else {
		rotated := path + ".1"
		if err := os.Rename(path, rotated); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(timedLine("/two", nginxTime(baseAt.Add(time.Minute)))), 0600); err != nil {
			t.Fatal(err)
		}
	}
	waitForBuckets(t, a, 2)

	missing := filepath.Join(dir, "later.log")
	b := NewAggregator(10)
	go Tailer{Path: missing, Aggregator: b, Retry: 10 * time.Millisecond}.Run(ctx)
	time.Sleep(25 * time.Millisecond)
	if err := os.WriteFile(missing, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	appendLine(t, missing, timedLine("/later", nginxTime(baseAt.Add(2*time.Minute))))
	waitForBuckets(t, b, 1)
}

func TestTailerPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timing.log")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a := NewAggregator(10)
	baseAt := time.Now().UTC().Truncate(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Tailer{Path: path, Aggregator: a, Retry: 10 * time.Millisecond}.Run(ctx)
	time.Sleep(30 * time.Millisecond)
	appendLine(t, path, `1 - "GET /partial HTTP/1.1" [`+nginxTime(baseAt)+`] status=200 rt=12`)
	time.Sleep(150 * time.Millisecond)
	if buckets, _ := a.Snapshot(); len(buckets) != 0 {
		t.Fatalf("partial line parsed: %#v", buckets)
	}
	appendLine(t, path, " uct=0.1 uht=8.5 urt=11 bytes=9\n")
	waitForBuckets(t, a, 1)
	buckets, _ := a.Snapshot()
	if buckets[0].UpstreamCount != 1 || buckets[0].UHTMax != 8.5 {
		t.Fatalf("partial line not joined: %#v", buckets[0])
	}

	// A residual old-file line must never be joined to the head of a rotated file.
	appendLine(t, path, `1 - "GET /stale HTTP/1.1" [`+nginxTime(baseAt.Add(time.Minute))+`] status=200 rt=12`)
	time.Sleep(30 * time.Millisecond)
	if runtime.GOOS == "windows" {
		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		appendLine(t, path, timedLine("/fresh", nginxTime(baseAt.Add(2*time.Minute))))
	} else {
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(timedLine("/fresh", nginxTime(baseAt.Add(2*time.Minute)))), 0600); err != nil {
			t.Fatal(err)
		}
	}
	waitForBuckets(t, a, 2)
	buckets, _ = a.Snapshot()
	if len(buckets) != 2 || buckets[1].RequestCount != 1 || buckets[1].UpstreamCount != 1 {
		t.Fatalf("pending survived rotation: %#v", buckets)
	}
}

func TestTailerReadErrorReopensAtOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timing.log")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a := NewAggregator(10)
	baseAt := time.Now().UTC().Truncate(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := true
	newReader := func(f *os.File) *bufio.Reader {
		if first {
			first = false
			return bufio.NewReaderSize(&failAfterNewlineReader{source: f}, 1)
		}
		return bufio.NewReader(f)
	}
	go Tailer{Path: path, Aggregator: a, Retry: 10 * time.Millisecond, newReader: newReader}.Run(ctx)
	time.Sleep(30 * time.Millisecond)
	appendLine(t, path, timedLine("/one", nginxTime(baseAt)))
	time.Sleep(80 * time.Millisecond)
	appendLine(t, path, timedLine("/two", nginxTime(baseAt.Add(time.Second))))
	waitForOpenRequestCount(t, a, 2)
	waitForBuckets(t, a, 1)
	buckets, _ := a.Snapshot()
	if buckets[0].RequestCount != 2 {
		t.Fatalf("same-file reopen replayed data: %#v", buckets[0])
	}
}

type failAfterNewlineReader struct {
	source io.Reader
	failed bool
}

func (r *failAfterNewlineReader) Read(p []byte) (int, error) {
	if r.failed {
		return 0, errors.New("injected read failure")
	}
	if len(p) > 1 {
		p = p[:1]
	}
	n, err := r.source.Read(p)
	if n == 1 && p[0] == '\n' {
		r.failed = true
	}
	return n, err
}

func timedLine(path, at string) string {
	return `1 - "GET ` + path + ` HTTP/1.1" [` + at + `] status=200 rt=12 uct=0.1 uht=8 urt=11 bytes=9` + "\n"
}

func nginxTime(at time.Time) string { return at.Format("02/Jan/2006:15:04:05 -0700") }
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.WriteString(line); e != nil {
		t.Fatal(e)
	}
}
func waitForBuckets(t *testing.T, a *Aggregator, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		var newest time.Time
		for bucketAt := range a.open {
			if newest.IsZero() || bucketAt.After(newest) {
				newest = bucketAt
			}
		}
		a.mu.Unlock()
		if !newest.IsZero() {
			a.Flush(newest.Add(time.Minute + bucketCloseGrace))
		}
		buckets, _ := a.Snapshot()
		if len(buckets) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	buckets, _ := a.Snapshot()
	t.Fatalf("buckets=%d want=%d", len(buckets), want)
}

func waitForOpenRequestCount(t *testing.T, a *Aggregator, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		var got int64
		for _, state := range a.open {
			got += state.bucket.RequestCount
		}
		a.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for open request count")
}
