package emulate

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/lhpqaq/all2api/internal/core"
)

func ParseActionBlocks(text string, cfg Config) ([]core.ToolCall, string, error) {
	if text == "" {
		return nil, "", nil
	}
	if cfg.MaxScanBytes > 0 && len(text) > cfg.MaxScanBytes {
		text = text[:cfg.MaxScanBytes]
	}

	opens := findOpenings(text)
	if len(opens) == 0 {
		return nil, strings.TrimSpace(text), nil
	}

	type span struct{ start, end int }
	spans := make([]span, 0)
	calls := make([]core.ToolCall, 0)

	for _, start := range opens {
		contentStart := start
		if i := strings.Index(text[start:], "\n"); i >= 0 {
			contentStart = start + i + 1
		}
		end := findClosingFence(text, contentStart)
		if end < 0 {
			continue
		}
		raw := strings.TrimSpace(text[contentStart:end])
		if raw == "" {
			continue
		}
		call, ok := parseToolCallJSON(raw, cfg)
		if ok {
			calls = append(calls, call)
			spans = append(spans, span{start: start, end: end + 3})
		}
	}

	if len(calls) == 0 {
		return nil, strings.TrimSpace(text), nil
	}

	clean := text
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		if sp.start < 0 || sp.end > len(clean) || sp.start >= sp.end {
			continue
		}
		clean = clean[:sp.start] + clean[sp.end:]
	}
	return calls, strings.TrimSpace(clean), nil
}

func findOpenings(text string) []int {
	out := make([]int, 0)
	idx := 0
	for {
		i := strings.Index(text[idx:], "```json")
		if i < 0 {
			break
		}
		pos := idx + i
		out = append(out, pos)
		idx = pos + len("```json")
	}
	return out
}

func findClosingFence(text string, from int) int {
	inString := false
	escape := false
	for i := from; i < len(text)-2; i++ {
		ch := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if text[i:i+3] == "```" {
			return i
		}
	}
	return -1
}

func parseToolCallJSON(raw string, cfg Config) (core.ToolCall, bool) {
	cleaned := normalizeJSON(raw, cfg)

	var obj map[string]any
	if err := json.Unmarshal([]byte(cleaned), &obj); err != nil {
		return core.ToolCall{}, false
	}
	name, _ := obj["tool"].(string)
	if name == "" {
		name, _ = obj["name"].(string)
	}
	if name == "" {
		return core.ToolCall{}, false
	}
	params, _ := obj["parameters"].(map[string]any)
	if params == nil {
		params, _ = obj["arguments"].(map[string]any)
	}
	if params == nil {
		params, _ = obj["input"].(map[string]any)
	}
	if params == nil {
		if s, ok := obj["parameters"].(string); ok && s != "" {
			_ = json.Unmarshal([]byte(s), &params)
		}
	}
	if params == nil {
		params = map[string]any{}
	}
	return core.ToolCall{ID: newCallID(), Name: name, Args: params}, true
}

func normalizeJSON(s string, cfg Config) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",\n}", "\n}")
	s = strings.ReplaceAll(s, ",\n]", "\n]")
	s = strings.ReplaceAll(s, ", }", " }")
	s = strings.ReplaceAll(s, ", ]", " ]")
	if cfg.SmartQuotes {
		s = replaceSmartQuotes(s)
	}
	return s
}

func replaceSmartQuotes(s string) string {
	repl := strings.NewReplacer(
		"\u201c", "\"", "\u201d", "\"", "\u201e", "\"", "\u201f", "\"",
		"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u00ab", "\"", "\u00bb", "\"",
	)
	s = strings.NewReplacer(
		"“", "\"", "”", "\"", "„", "\"", "‟", "\"",
		"‘", "'", "’", "'", "‚", "'", "‛", "'",
		"«", "\"", "»", "\"",
	).Replace(s)
	return repl.Replace(s)
}

var callSeq uint64

func newCallID() string {
	seq := atomic.AddUint64(&callSeq, 1)
	return "call_" + strconv.FormatUint(seq, 10)
}
