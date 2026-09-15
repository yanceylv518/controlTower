package dashboard

import (
	"controltower/server/internal/billing"
	"controltower/server/internal/xlsxwriter"
)

func statementInputLabel(job billing.Job) string {
	if job.UsageVersion > 0 {
		return "普通输入 Token"
	}
	return "输入 Token"
}
func statementOutputLabel(job billing.Job) string {
	if job.UsageVersion > 0 {
		return "普通输出 Token"
	}
	return "输出 Token"
}
func multimediaHeaders() []xlsxwriter.Cell {
	return []xlsxwriter.Cell{t("图像输入 Token"), t("图像输出 Token"), t("音频输入 Token"), t("音频输出 Token")}
}
func multimediaCells(u billing.MultimediaUsage) []xlsxwriter.Cell {
	return []xlsxwriter.Cell{n64(u.ImageInputTokens), n64(u.ImageOutputTokens), n64(u.AudioInputTokens), n64(u.AudioOutputTokens)}
}
func statementMediaWidths(job billing.Job, widths []float64) []float64 {
	if job.UsageVersion > 0 {
		return append(widths, 18, 18, 18, 18)
	}
	return widths
}
func statementMediaHeaders(job billing.Job, cells []xlsxwriter.Cell) []xlsxwriter.Cell {
	if job.UsageVersion > 0 {
		return append(cells, multimediaHeaders()...)
	}
	return cells
}
func statementMediaCells(job billing.Job, usage billing.MultimediaUsage, cells []xlsxwriter.Cell) []xlsxwriter.Cell {
	if job.UsageVersion > 0 {
		return append(cells, multimediaCells(usage)...)
	}
	return cells
}
