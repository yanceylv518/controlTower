package containerlogs

import (
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

const continuationTTL = 10 * time.Minute

type indexSnapshot struct {
	name      string
	info      os.FileInfo
	signature string
}
type cachedIndex struct {
	snapshot indexSnapshot
	index    *sparseIndex
}
type searchSession struct {
	query             cl.Query
	dir               string
	files             []indexSnapshot
	filePos           int
	index             *sparseIndex
	spanPos           int
	readPos           int64
	parser            *recordParser
	compressed        *gzip.Reader
	compressedFile    *os.File
	pending           []string
	skipped           bool
	partial           bool
	indexedBytes      int64
	current, previous string
	lastResult        cl.Result
	expires           time.Time
}

// Indexes and cursors live only in this privileged local process. The Agent
// never receives file paths or an index; restart/expiry fails an old cursor.
type IndexEngine struct {
	sessions  []*searchSession
	cache     map[string]cachedIndex
	pageBytes int64
}

func NewIndexEngine() *IndexEngine {
	return &IndexEngine{cache: map[string]cachedIndex{}, pageBytes: fileScanLimit}
}

func (e *IndexEngine) Release(cursor string) {
	if cursor == "" {
		return
	}
	for n, s := range e.sessions {
		if cursor == s.current || cursor == s.previous {
			s.close()
			e.sessions = append(e.sessions[:n], e.sessions[n+1:]...)
			return
		}
	}
}
func (s *searchSession) close() {
	if s.compressed != nil {
		s.compressed.Close()
		s.compressed = nil
	}
	if s.compressedFile != nil {
		s.compressedFile.Close()
		s.compressedFile = nil
	}
}
func (s *searchSession) nextFile() {
	s.close()
	s.filePos++
	s.index = nil
	s.spanPos = 0
	s.readPos = 0
	s.parser = nil
}

func snapshotSignature(f *os.File, size int64) (string, error) {
	hash := sha256.New()
	for _, off := range []int64{0, max(0, size-128)} {
		b := make([]byte, min(int64(128), size-off))
		if _, e := f.ReadAt(b, off); e != nil {
			return "", e
		}
		hash.Write(b)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func snapshotMatches(f *os.File, s indexSnapshot) bool {
	info, e := f.Stat()
	if e != nil || !os.SameFile(info, s.info) || info.Size() < s.info.Size() {
		return false
	}
	// Append-only logs may grow after the query's size snapshot. Same-size edits
	// and the usual copy-truncate/recreate rotation must invalidate it.
	if info.Size() == s.info.Size() && !info.ModTime().Equal(s.info.ModTime()) {
		return false
	}
	signature, e := snapshotSignature(f, s.info.Size())
	return e == nil && signature == s.signature
}
func queryIdentity(q cl.Query) string { q.Cursor = ""; b, _ := json.Marshal(q); return string(b) }

func (e *IndexEngine) Query(ctx context.Context, dir string, q cl.Query, loc *time.Location) cl.Result {
	now := time.Now()
	live := e.sessions[:0]
	for _, s := range e.sessions {
		if now.After(s.expires) {
			s.close()
		} else {
			live = append(live, s)
		}
	}
	e.sessions = live
	fail := func(message string) cl.Result { return cl.Result{Status: "failed", Lines: []string{}, Error: message} }
	var session *searchSession
	if q.Cursor != "" {
		for _, s := range e.sessions {
			if q.Cursor != s.current && q.Cursor != s.previous {
				continue
			}
			if s.dir != dir || queryIdentity(s.query) != queryIdentity(q) {
				return fail("继续查询的条件或来源不一致，请重新查询")
			}
			s.expires = now.Add(continuationTTL)
			if q.Cursor == s.previous {
				return s.lastResult
			}
			session = s
			break
		}
		if session == nil {
			return fail("查询进度已过期或读取服务已重启，请重新查询")
		}
	} else {
		if len(e.sessions) >= 16 {
			for n, old := range e.sessions {
				if old.current == "" {
					old.close()
					e.sessions = append(e.sessions[:n], e.sessions[n+1:]...)
					break
				}
			}
		}
		if len(e.sessions) >= 16 {
			return fail("进行中的查询过多，请稍后重试（进度保留 10 分钟）")
		}
		session = &searchSession{query: q, dir: dir, expires: now.Add(continuationTTL)}
		if err := session.snapshot(ctx); err != nil {
			return fail("无法建立日志文件快照，请检查目录读取权限")
		}
		e.sessions = append(e.sessions, session)
	}
	// Leave time for discovery and serializing the page before the broker timeout.
	pageCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result := e.page(pageCtx, session, loc)
	result.IndexedBytes = session.indexedBytes
	if result.Status == "failed" {
		session.close()
	}
	if result.Status == "succeeded" && (session.filePos < len(session.files) || len(session.pending) > 0) {
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return fail("无法生成查询进度，请重试")
		}
		result.NextCursor = hex.EncodeToString(token)
		result.Note = "本批尚未查完，Agent 将从当前进度自动处理下一批。"
	} else if result.Status == "succeeded" {
		result.Complete = true
		result.Phase = "complete"
		if len(session.files) == 0 {
			result.Note = "日志目录中没有可查询的日志文件"
		}
	}
	result.Truncated = session.partial || session.skipped
	if session.skipped {
		result.Note += " 部分记录时间无法识别或超过单条大小限制，已跳过，结果可能不完整。"
	}
	if session.partial {
		result.Note += " 目录遍历达到保护限制，未覆盖全部目录。"
	}
	session.previous = q.Cursor
	session.current = result.NextCursor
	session.lastResult = result
	// Retain one last-page reply for idempotent retries, but release stream handles.
	if result.NextCursor == "" {
		session.close()
	}
	return result
}

func (s *searchSession) snapshot(ctx context.Context) error {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		count++
		if count > 4096 {
			s.partial = true
			return fs.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if strings.Count(name, "/") >= 3 {
				s.partial = true
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !logName.MatchString(d.Name()) {
			return nil
		}
		file, err := openLogFile(root, name)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		signature, err := snapshotSignature(file, info.Size())
		if err != nil {
			return err
		}
		s.files = append(s.files, indexSnapshot{name, info, signature})
		return nil
	})
	sort.Slice(s.files, func(i, j int) bool {
		a, b := s.files[i], s.files[j]
		az, bz := strings.HasSuffix(strings.ToLower(a.name), ".gz"), strings.HasSuffix(strings.ToLower(b.name), ".gz")
		if az != bz {
			return !az
		}
		if a.info.ModTime().Equal(b.info.ModTime()) {
			return a.name < b.name
		}
		return a.info.ModTime().After(b.info.ModTime())
	})
	return err
}

func (e *IndexEngine) cached(file *os.File, s *searchSession, snap indexSnapshot, loc *time.Location) *sparseIndex {
	old, ok := e.cache[s.query.SourceID+"\n"+snap.name]
	if ok && snapshotMatches(file, old.snapshot) {
		if snap.info.Size() == old.snapshot.info.Size() {
			return old.index
		}
		// Rebuild the final span because an append may extend its last record/line.
		if len(old.index.spans) > 0 {
			start := old.index.spans[len(old.index.spans)-1].start
			index := newSparseIndex(start, loc)
			index.spans = append(index.spans, old.index.spans[:len(old.index.spans)-1]...)
			index.blockSize = old.index.blockSize
			index.skipped = old.index.skipped
			return index
		}
	}
	return newSparseIndex(0, loc)
}

func (e *IndexEngine) page(ctx context.Context, s *searchSession, loc *time.Location) cl.Result {
	result := cl.Result{Status: "succeeded", Lines: []string{}, Phase: "querying"}
	total := 0
	buf := make([]byte, 64*1024)
	seen := map[int]bool{}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		result.Status = "failed"
		result.Error = "日志目录无法读取"
		return result
	}
	defer root.Close()
	matches := newMatcher(s.query)
	emit := func(record indexedRecord) {
		if record.oversized || record.stamp.IsZero() {
			s.skipped = true
			return
		}
		if record.stamp.Before(s.query.From) || !record.stamp.Before(s.query.To) {
			return
		}
		raw := make([]string, len(record.lines))
		for i, line := range record.lines {
			raw[i] = line.text
		}
		if !matches(strings.Join(raw, "\n")) {
			return
		}
		for _, line := range record.lines {
			s.pending = append(s.pending, fmt.Sprintf("%s [byte %d] %s", s.files[s.filePos].name, line.offset, Redact(line.text)))
		}
	}
	for {
		for len(s.pending) > 0 {
			line := s.pending[0]
			if len(line) > cl.MaxResultBytes {
				s.skipped = true
				s.pending = s.pending[1:]
				continue
			}
			if len(result.Lines) >= cl.MaxLines || total+len(line) > cl.MaxResultBytes {
				return result
			}
			result.Lines = append(result.Lines, line)
			total += len(line)
			s.pending = s.pending[1:]
		}
		if s.filePos >= len(s.files) {
			return result
		}
		if ctx.Err() != nil || result.ScannedBytes >= e.pageBytes {
			return result
		}
		if !seen[s.filePos] && len(seen) >= maxLogFiles {
			return result
		}
		if !seen[s.filePos] {
			seen[s.filePos] = true
			result.FilesScanned++
		}
		snap := s.files[s.filePos]
		file, err := openLogFile(root, snap.name)
		if err != nil || !snapshotMatchesSafe(file, snap) {
			if file != nil {
				file.Close()
			}
			result.Status = "failed"
			result.Error = "日志文件已轮转、截断或不可读，请重新查询"
			return result
		}
		if strings.HasSuffix(strings.ToLower(snap.name), ".gz") {
			result.Phase = "archive"
			if s.compressed == nil {
				s.compressedFile = file
				// The compressed stream is bounded to the query's immutable size snapshot.
				s.compressed, err = gzip.NewReader(io.NewSectionReader(file, 0, snap.info.Size()))
				if err != nil {
					s.close()
					result.Status = "failed"
					result.Error = "压缩日志损坏或格式不支持"
					return result
				}
				s.parser = &recordParser{loc: loc}
			} else {
				file.Close()
			}
			n, readErr := s.compressed.Read(buf[:min(int64(len(buf)), e.pageBytes-result.ScannedBytes)])
			result.ScannedBytes += int64(n)
			s.parser.feed(buf[:n], readErr == io.EOF, emit)
			if readErr == io.EOF {
				s.nextFile()
			} else if readErr != nil {
				result.Status = "failed"
				result.Error = "压缩日志解压失败，查询未完成"
				return result
			}
			continue
		}
		if s.index == nil {
			s.index = e.cached(file, s, snap, loc)
		}
		index := s.index
		if !index.complete {
			result.Phase = "indexing"
			count := min(int64(len(buf)), snap.info.Size()-index.offset, e.pageBytes-result.ScannedBytes)
			n, readErr := file.ReadAt(buf[:count], index.offset)
			file.Close()
			if readErr != nil && !(readErr == io.EOF && int64(n) == count) {
				result.Status = "failed"
				result.Error = "索引建立期间日志发生变化，请重新查询"
				return result
			}
			index.offset += int64(n)
			result.ScannedBytes += int64(n)
			s.indexedBytes += int64(n)
			index.parser.feed(buf[:n], index.offset == snap.info.Size(), index.add)
			if index.offset == snap.info.Size() {
				index.flush()
				index.complete = true
				s.skipped = s.skipped || index.skipped
				if len(e.cache) >= 32 {
					for key := range e.cache {
						delete(e.cache, key)
						break
					}
				}
				e.cache[s.query.SourceID+"\n"+snap.name] = cachedIndex{snap, index}
			}
			continue
		}
		result.Phase = "querying"
		s.skipped = s.skipped || index.skipped
		if s.spanPos >= len(index.spans) {
			file.Close()
			s.nextFile()
			continue
		}
		span := index.spans[s.spanPos]
		if span.min.IsZero() || span.max.Before(s.query.From) || !span.min.Before(s.query.To) {
			file.Close()
			s.spanPos++
			continue
		}
		if s.parser == nil {
			s.readPos = span.start
			s.parser = &recordParser{offset: span.start, lineStart: span.start, loc: loc}
		}
		count := min(int64(len(buf)), span.end-s.readPos, e.pageBytes-result.ScannedBytes)
		n, readErr := file.ReadAt(buf[:count], s.readPos)
		file.Close()
		if readErr != nil {
			result.Status = "failed"
			result.Error = "日志区间读取失败，请重新查询"
			return result
		}
		s.readPos += int64(n)
		result.ScannedBytes += int64(n)
		s.parser.feed(buf[:n], s.readPos == span.end, emit)
		if s.readPos == span.end {
			s.parser = nil
			s.spanPos++
		}
	}
}
func snapshotMatchesSafe(file *os.File, snap indexSnapshot) bool {
	return file != nil && snapshotMatches(file, snap)
}
