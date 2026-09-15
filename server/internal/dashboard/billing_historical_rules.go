package dashboard

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"controltower/server/internal/billing"
)

// Only CT-generated files are read here: its sheet IDs map directly to the
// sheetN.xml entries. No request-detail XML is inflated for new-format files.
func historicalPriceSheetPath(files []*zip.File) (string, error) {
	for _, file := range files {
		if file.Name != "xl/workbook.xml" {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return "", err
		}
		var book struct {
			Sheets []struct {
				Name string `xml:"name,attr"`
				ID   int    `xml:"sheetId,attr"`
			} `xml:"sheets>sheet"`
		}
		err = xml.NewDecoder(in).Decode(&book)
		in.Close()
		if err != nil {
			return "", err
		}
		for _, sheet := range book.Sheets {
			if sheet.Name == "历史计价规则" {
				return fmt.Sprintf("xl/worksheets/sheet%d.xml", sheet.ID), nil
			}
		}
	}
	return "", nil
}

func (prices statementPrices) rules(job billing.Job, row billing.StatementAggregateRow) string {
	if job.UsesNewAPICharge() && job.UsageVersion < billing.HistoricalPriceUsageVersion {
		return "旧账单未保存历史单价和规则，请新建账单任务"
	}
	if prices == nil {
		return "待加载"
	}
	key := statementPriceKey{Day: row.Day.In(billing.BusinessLocation).Format("2006-01-02"), Model: row.ModelName}
	if job.JobType == "upstream_statement" {
		key.Channel = row.ChannelID
	}
	items := map[string]bool{}
	for tuple := range prices[key] {
		rule := tuple[4]
		if rule == "" {
			rule = fmt.Sprintf("历史单价（金额/百万 Token）：输入 %s；输出 %s；缓存读取 %s；缓存写入 %s；表达式/条件未记录", tuple[0], tuple[1], tuple[2], tuple[3])
			rule = strings.ReplaceAll(rule, unusedStatementPrice, "未使用")
		}
		items[rule] = true
	}
	if len(items) == 0 {
		return "历史计价规则未记录"
	}
	ordered := make([]string, 0, len(items))
	for rule := range items {
		ordered = append(ordered, rule)
	}
	sort.Strings(ordered)
	ordered, ambiguous := groupRuleCompleteness(ordered)
	if len(ordered) == 1 {
		return ordered[0]
	}
	for i := range ordered {
		ordered[i] = fmt.Sprintf("规则 %d：\n%s", i+1, ordered[i])
	}
	if ambiguous {
		return "当日存在不同已记录价格，以下含信息不完整的记录（记录条数不代表价格套数）\n" + strings.Join(ordered, "\n\n")
	}
	return fmt.Sprintf("当日 %d 套计价规则（逐套列出，不代表统一单价）\n", len(ordered)) + strings.Join(ordered, "\n\n")
}
