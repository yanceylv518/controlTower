package billing

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dailyFileStoreStub struct {
	groups  []UserDailyFile
	details []RequestDetail
	files   []UserDailyFile
}

func (s *dailyFileStoreStub) ListBillingRequestDetailGroups(context.Context, string) ([]UserDailyFile, error) {
	return s.groups, nil
}
func (s *dailyFileStoreStub) ListBillingRequestDetails(context.Context, string, time.Time, int64) ([]RequestDetail, error) {
	return s.details, nil
}
func (s *dailyFileStoreStub) PutBillingUserDailyFile(_ context.Context, file UserDailyFile) error {
	s.files = append(s.files, file)
	return nil
}
func (s *dailyFileStoreStub) ListBillingChannelRequestDetailGroups(context.Context, string) ([]ChannelDailyFile, error) {
	return nil, nil
}
func (s *dailyFileStoreStub) ListBillingChannelRequestDetails(context.Context, string, time.Time, int64) ([]RequestDetail, error) {
	return nil, nil
}
func (s *dailyFileStoreStub) ListBillingChannelAnomalyDetails(context.Context, string, time.Time, int64) ([]AnomalyOrder, error) {
	return nil, nil
}
func (s *dailyFileStoreStub) PutBillingChannelDailyFile(context.Context, ChannelDailyFile) error {
	return nil
}

func TestUserDailyFileGeneratorWritesApprovedColumns(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, BusinessLocation)
	store := &dailyFileStoreStub{
		groups: []UserDailyFile{{BillDay: day, UserID: 7}},
		details: []RequestDetail{{
			CreatedUnix:  day.Unix(),
			RequestID:    "req-1",
			Username:     "alice",
			TokenName:    "prod",
			ChannelID:    3,
			ChannelName:  "primary",
			ModelName:    "claude-test",
			PromptTokens: 10,
			Charge:       LogCharge{Mode: "token", InputPrice: "2.000000", InputAmount: "0.000020", Total: "0.000020"},
		}},
	}
	root := t.TempDir()
	job := Job{ID: "job1", InstanceID: "site-a"}
	if err := (UserDailyFileGenerator{Store: store, Root: root}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(store.files) != 1 || store.files[0].SHA256 == "" || store.files[0].FileSize == 0 {
		t.Fatalf("unexpected file metadata: %+v", store.files)
	}
	content := workbookText(t, filepath.Join(root, filepath.FromSlash(store.files[0].RelativePath)))
	for _, wanted := range []string{"请求 ID", "输入 Token", "输入单价", "总费用", "claude-test"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("workbook missing %q", wanted)
		}
	}
	for _, forbidden := range []string{"quota", "Quota", "倍率", "实际模型", "上游请求 ID", "请求路径", "是否流式", "响应耗时", "首字响应时间", "总 Token", "按次单价", "普通缓存写入", "5m 写入", "1h 写入"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("workbook exposes forbidden field %q", forbidden)
		}
	}
	// A retry must reuse the immutable job file and remain successful.
	if err := (UserDailyFileGenerator{Store: store, Root: root}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
}

func TestUserDailyFileGeneratorPublishesFromDiskSpool(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, BusinessLocation)
	store := &dailyFileStoreStub{}
	spool := FileDetailSpool{Root: filepath.Join(t.TempDir(), "spool")}
	job := Job{ID: "0123456789abcdef0123456789abcdef", InstanceID: "site-a", UserID: 7}
	row := RequestDetail{JobID: job.ID, InstanceID: job.InstanceID, BillDay: day, CreatedUnix: day.Unix(), SourceLogID: 10, UserID: 7, Username: "alice", ModelName: "model-a", Charge: LogCharge{Mode: "token", Total: "0.25"}}
	if err := spool.WritePage(context.Background(), job, JobStep{StepNo: 0}, LogCursor{CreatedUnix: row.CreatedUnix, ID: row.SourceLogID}, []RequestDetail{row}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "files")
	if err := (UserDailyFileGenerator{Store: store, Root: root, Spool: spool}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(store.files) != 1 || store.files[0].FileSize == 0 || store.files[0].SHA256 == "" {
		t.Fatalf("unexpected file metadata: %+v", store.files)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(store.files[0].RelativePath))); err != nil {
		t.Fatal(err)
	}
}

func TestUserDailyWorkbookShowsOnlyApplicableOptionalPrices(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, BusinessLocation)
	path := filepath.Join(t.TempDir(), "details.xlsx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []RequestDetail{
		{CreatedUnix: day.Unix(), Charge: LogCharge{Mode: "per_request", PerRequestPrice: "0.25", Total: "0.25"}},
		{CreatedUnix: day.Unix(), CacheWrite5mTokens: 10, Charge: LogCharge{Mode: "tiered_expr", CacheWrite5mPrice: "1.8", Total: "0.000018"}},
	}
	if err = WriteUserDailyWorkbook(file, Job{InstanceID: "site-a"}, UserDailyFile{BillDay: day, UserID: 7}, rows); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	content := workbookText(t, path)
	for _, wanted := range []string{"按次单价", "按次单价单位：金额/次", "5m 写入 Token", "5m 写入单价"} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("workbook missing applicable field %q", wanted)
		}
	}
	for _, forbidden := range []string{"普通缓存写入", "1h 写入"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("workbook exposes inapplicable field %q", forbidden)
		}
	}
}

func workbookText(t *testing.T, path string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out strings.Builder
	for _, file := range r.File {
		if !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, _ = io.Copy(&out, reader)
		_ = reader.Close()
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
