package notificationtime

import "time"

// 通知固定使用 UTC+8，不依赖主机时区或镜像中的 tzdata。
var beijing = time.FixedZone("UTC+8", 8*60*60)

func Format(value time.Time) string {
	return value.In(beijing).Format("2006-01-02 15:04:05") + "（北京时间 UTC+8）"
}
