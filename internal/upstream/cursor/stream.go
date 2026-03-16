package cursor

import (
	"strings"
)

type streamExtractFSM struct {
	buffer     string
	inThinking bool
}

func (f *streamExtractFSM) Process(delta string) (textOut string, thinkingOut string) {
	f.buffer += delta

	for {
		if f.inThinking {
			idx := strings.Index(f.buffer, "</thinking>")
			if idx != -1 {
				thinkingOut += f.buffer[:idx]
				f.buffer = f.buffer[idx+len("</thinking>"):]
				f.inThinking = false
				continue
			}

			// Check partial match
			keepLen := 0
			for i := 1; i <= 10; i++ {
				if len(f.buffer) >= i && strings.HasPrefix("</thinking>", f.buffer[len(f.buffer)-i:]) {
					keepLen = i
					break
				}
			}
			if keepLen > 0 {
				thinkingOut += f.buffer[:len(f.buffer)-keepLen]
				f.buffer = f.buffer[len(f.buffer)-keepLen:]
				return
			}

			thinkingOut += f.buffer
			f.buffer = ""
			return
		}

		idx := strings.Index(f.buffer, "<thinking>")
		if idx != -1 {
			textOut += f.buffer[:idx]
			// Sometimes model hallucinates ```<thinking>
			if strings.HasSuffix(textOut, "```") {
				textOut = textOut[:len(textOut)-3]
			} else if strings.HasSuffix(textOut, "```\n") {
				textOut = textOut[:len(textOut)-4]
			}

			f.buffer = f.buffer[idx+len("<thinking>"):]
			f.inThinking = true
			continue
		}

		// Check partial `<thinking>` or partial ` ```<thinking>`
		keepLen := 0
		checkTarget := "<thinking>"
		for i := 1; i <= 9; i++ {
			if len(f.buffer) >= i && strings.HasPrefix(checkTarget, f.buffer[len(f.buffer)-i:]) {
				keepLen = i
				break
			}
		}

		if keepLen > 0 {
			textOut += f.buffer[:len(f.buffer)-keepLen]
			f.buffer = f.buffer[len(f.buffer)-keepLen:]
			return
		}

		textOut += f.buffer
		f.buffer = ""
		return
	}
}

func (f *streamExtractFSM) Flush() (textOut string, thinkingOut string) {
	if f.inThinking {
		thinkingOut = f.buffer
	} else {
		textOut = f.buffer
	}
	f.buffer = ""
	return
}
