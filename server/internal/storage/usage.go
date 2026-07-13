package storage

type UsageRow struct {
	DimensionType    string
	DimensionKey     string
	RequestCount     int64
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}
