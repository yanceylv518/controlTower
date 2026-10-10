package archivefacts

// ParseUsageSnapshot applies the same request-level usage projection as immutable
// billing facts, without requiring identity/evidence fields. Callers must retain
// raw quantities and actual quota independently; missing fields stay nullable.
func ParseUsageSnapshot(values map[string][]byte) Fact {
	p := parser{values: values, states: map[string]string{}, issues: map[string]bool{}}
	f := Fact{SourcePromptTokens: p.integer("prompt_tokens"), SourceCompletionTokens: p.integer("completion_tokens"), TokenNameSnapshot: p.text("token_name")}
	if (f.SourcePromptTokens != nil && *f.SourcePromptTokens < 0) || (f.SourceCompletionTokens != nil && *f.SourceCompletionTokens < 0) {
		p.issue("token_value_invalid")
	}
	p.parseUsage(&f)
	f.IssueCodes = p.sortedIssues()
	f.ParseState = "complete"
	if len(f.IssueCodes) > 0 {
		f.ParseState = "issues"
	}
	return f
}
