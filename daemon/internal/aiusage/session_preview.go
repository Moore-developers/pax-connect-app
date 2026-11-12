package aiusage

import (
	"encoding/json"
	"strings"
)

// claudeUserPreview recognizes the same native human text shapes as turn parsing.
// Tool results and local command output are not user messages to preview.
func claudeUserPreview(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []struct{ Type, Text string }
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, block := range blocks {
			if block.Type == "tool_result" {
				return ""
			}
			if block.Type == "text" {
				parts = append(parts, block.Text)
			}
		}
		text = strings.Join(parts, " ")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<local-command-stdout>") || strings.HasPrefix(text, "<local-command-stderr>") {
		return ""
	}
	if rest, found := strings.CutPrefix(text, "<command-name>"); found {
		if command, _, closed := strings.Cut(rest, "</command-name>"); closed {
			return cleanSessionTitle(command)
		}
	}
	return cleanSessionTitle(text)
}
