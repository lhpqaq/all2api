package emulate

import (
	"regexp"
	"sort"
	"strings"
)

var (
	// Clean up optional markdown wrapper around thinking tags
	tickOpenRe  = regexp.MustCompile("(?m)^`{1,3}\\s*<thinking>")
	tickCloseRe = regexp.MustCompile("(?m)</thinking>\\s*`{1,3}$")

	thinkingRe = regexp.MustCompile("(?s)<thinking>([\\s\\S]*?)</thinking>")
)

type block struct {
	start int
	end   int
}

// ExtractThinking extracts all thinking content and returns the combined thinking string and the cleaned text.
func ExtractThinking(text string) (string, string) {
	if !strings.Contains(text, "<thinking>") {
		return "", text
	}

	text = tickOpenRe.ReplaceAllString(text, "<thinking>")
	text = tickCloseRe.ReplaceAllString(text, "</thinking>")

	var thinkingBlocks []string
	var ranges []block

	matches := thinkingRe.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		if len(m) >= 4 {
			content := strings.TrimSpace(text[m[2]:m[3]])
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimPrefix(content, "``")
			content = strings.TrimPrefix(content, "`")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSuffix(content, "``")
			content = strings.TrimSuffix(content, "`")
			content = strings.TrimSpace(content)
			if content != "" {
				thinkingBlocks = append(thinkingBlocks, content)
			}
			ranges = append(ranges, block{start: m[0], end: m[1]})
		}
	}

	lastOpen := strings.LastIndex(text, "<thinking>")
	lastClose := strings.LastIndex(text, "</thinking>")
	if lastOpen >= 0 && (lastClose < 0 || lastOpen > lastClose) {
		unclosed := strings.TrimSpace(text[lastOpen+len("<thinking>"):])
		unclosed = strings.TrimPrefix(unclosed, "```")
		unclosed = strings.TrimPrefix(unclosed, "``")
		unclosed = strings.TrimPrefix(unclosed, "`")
		unclosed = strings.TrimSuffix(unclosed, "```")
		unclosed = strings.TrimSuffix(unclosed, "``")
		unclosed = strings.TrimSuffix(unclosed, "`")
		unclosed = strings.TrimSpace(unclosed)
		if unclosed != "" {
			thinkingBlocks = append(thinkingBlocks, unclosed)
		}
		ranges = append(ranges, block{start: lastOpen, end: len(text)})
	}

	// Remove ranges from text bottom up
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start > ranges[j].start
	})

	clean := text
	for _, r := range ranges {
		clean = clean[:r.start] + clean[r.end:]
	}

	clean = regexp.MustCompile("\\n{3,}").ReplaceAllString(clean, "\n\n")
	clean = strings.TrimSpace(clean)

	clean = regexp.MustCompile("(?m)^`{1,3}\\s*\\n").ReplaceAllString(clean, "")
	clean = regexp.MustCompile("(?m)\\n\\s*`{1,3}$").ReplaceAllString(clean, "")

	if strings.HasPrefix(clean, "`") && strings.HasSuffix(clean, "`") && !strings.HasPrefix(clean, "``") {
		if strings.Count(clean, "`") == 2 {
			clean = clean[1 : len(clean)-1]
		}
	}
	clean = strings.TrimSpace(clean)

	return strings.Join(thinkingBlocks, "\n\n"), clean
}

// ThinkingHint defines the instruction to inject into the system prompt.
const ThinkingHint = `You may think through your approach inside <thinking>...</thinking> tags before responding. This thinking will not be shown to the user. Feel free to use it to analyze the request.
CRITICAL WARNING: The execution environment has a strict 60-second / 4000-token hard limit! 
You MUST keep your thinking extremely concise (under 1500 tokens). DO NOT do overly long analysis. 
You MUST close the </thinking> tag and write your actual response well before the limit, otherwise your output will be permanently truncated and the user will get nothing! Keep it brief.`
