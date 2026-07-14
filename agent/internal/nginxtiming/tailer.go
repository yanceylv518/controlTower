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
	newReader  func(*os.File) *bufio.Reader
}

const maxPendingLineBytes = 64 * 1024

func (t Tailer) Run(ctx context.Context) {
	retry := t.Retry
	if retry <= 0 {
		retry = 30 * time.Second
	}
	var file *os.File
	var reader *bufio.Reader
	var openedInfo os.FileInfo
	var offset int64
	var pending string
	discardPending := false
	firstOpen := true
	reopenAtStart := false
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
			if firstOpen {
				offset, _ = opened.Seek(0, io.SeekEnd)
				firstOpen = false
			} else if reopenAtStart {
				offset, _ = opened.Seek(0, io.SeekStart)
				pending = ""
				reopenAtStart = false
			} else {
				if _, err = opened.Seek(offset, io.SeekStart); err != nil {
					_ = opened.Close()
					if !waitContext(ctx, retry) {
						return
					}
					continue
				}
			}
			reader = bufio.NewReader(opened)
			if t.newReader != nil {
				reader = t.newReader(opened)
			}
			file, openedInfo, warned = opened, info, false
		}

		chunk, err := reader.ReadString('\n')
		if len(chunk) > 0 {
			offset += int64(len(chunk))
			line := pending + chunk
			if discardPending {
				if strings.HasSuffix(chunk, "\n") {
					discardPending = false
				}
			} else if strings.HasSuffix(line, "\n") {
				pending = ""
				if entry, ok := ParseLine(strings.TrimSpace(line)); ok {
					t.Aggregator.Add(entry)
					parsed++
				} else {
					skipped++
				}
			} else if len(line) <= maxPendingLineBytes {
				pending = line
			} else {
				pending = ""
				discardPending = true
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
				reopenAtStart = true
				pending = ""
				discardPending = false
				continue
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
