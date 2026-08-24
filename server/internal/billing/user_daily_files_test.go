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
	for _, forbidden := range []string{"quota", "Quota", "倍率", "实际模型", "上游请求 ID", "请求路径", "是否流式", "响应耗时", "首字响应时间", "总 Token"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("workbook exposes forbidden field %q", forbidden)
		}
	}
	// A retry must reuse the immutable job file and remain successful.
	if err := (UserDailyFileGenerator{Store: store, Root: root}).GenerateJobFiles(context.Background(), job); err != nil {
		t.Fatal(err)
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
