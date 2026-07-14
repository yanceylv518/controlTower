package nginxtiming

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

type Tailer struct {
	Path       string
	Aggregator *Aggregator
	Retry      time.Duration
}

func (t Tailer) Run(ctx context.Context) {
	retry := t.Retry
	if retry <= 0 {
		retry = 30 * time.Second
	}
	var file *os.File
	var reader *bufio.Reader
	var openedInfo os.FileInfo
	var offset int64
	var parsed, skipped int64
	warned := false
	statsTicker := time.NewTicker(10 * time.Minute)
	flushTicker := time.NewTicker(time.Second)
	defer statsTicker.Stop()
	defer flushTicker.Stop()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	for {
		if file == nil {
			opened, err := os.Open(t.Path)
			if err != nil {
				if !warned {
					log.Printf("WARN nginx timing log unavailable; retrying: %v", err)
					warned = true
				}
				if !waitContext(ctx, retry) {
					return
				}
				continue
			}
			info, err := opened.Stat()
			if err != nil {
				_ = opened.Close()
				if !waitContext(ctx, retry) {
					return
				}
				continue
			}
			if offset == 0 {
				offset, _ = opened.Seek(0, io.SeekEnd)
			} else {
				offset, _ = opened.Seek(0, io.SeekStart)
			}
			file, reader, openedInfo, warned = opened, bufio.NewReader(opened), info, false
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			offset += int64(len(line))
			if entry, ok := ParseLine(strings.TrimSpace(line)); ok {
				t.Aggregator.Add(entry)
				parsed++
			} else {
				skipped++
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			_ = file.Close()
			file = nil
		}
		if errors.Is(err, io.EOF) {
			info, statErr := os.Stat(t.Path)
			if statErr != nil || !os.SameFile(openedInfo, info) || (info != nil && info.Size() < offset) {
				_ = file.Close()
				file = nil
				offset = 1
			}
			select {
			case <-ctx.Done():
				return
			case <-statsTicker.C:
				log.Printf("nginx timing tail stats parsed=%d skipped=%d", parsed, skipped)
				parsed, skipped = 0, 0
			case <-flushTicker.C:
				t.Aggregator.Flush(time.Now().UTC())
			}
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
