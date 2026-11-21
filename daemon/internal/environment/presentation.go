package environment

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

//go:embed browser_texts.json
var browserTextsJSON []byte

// Presentation is per-run display data. It never controls targets or recorded evidence.
type Presentation struct {
	Locale   string            `json:"locale"`
	Messages map[string]string `json:"messages"`
}

func browserCatalog() map[string]map[string]string {
	var catalog map[string]map[string]string
	if err := json.Unmarshal(browserTextsJSON, &catalog); err != nil {
		panic("invalid built-in browser catalog")
	}
	return catalog
}

// ValidatePresentation runs at the IPC trust boundary; no text is interpreted as markup.
func ValidatePresentation(p *Presentation) error {
	if p == nil {
		return nil
	}
	catalog := browserCatalog()[p.Locale]
	if catalog == nil || len(p.Messages) > 128 {
		return errors.New("browser_presentation_invalid")
	}
	size := 0
	for key, value := range p.Messages {
		original, ok := catalog[key]
		if !ok || strings.TrimSpace(value) == "" || len(value) > 2048 {
			return errors.New("browser_presentation_invalid")
		}
		actual, valid := templateArguments(value)
		expected, _ := templateArguments(original)
		if !valid || len(actual) != len(expected) {
			return errors.New("browser_presentation_invalid")
		}
		for name := range expected {
			if !actual[name] {
				return errors.New("browser_presentation_invalid")
			}
		}
		size += len(key) + len(value)
	}
	if size > 32768 {
		return errors.New("browser_presentation_invalid")
	}
	return nil
}

func templateArguments(text string) (map[string]bool, bool) {
	args := map[string]bool{}
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch != '{' && ch != '}' {
			if ch < 32 && ch != '\n' && ch != '\t' {
				return nil, false
			}
			continue
		}
		if i+1 < len(text) && text[i+1] == ch {
			i++
			continue
		}
		if ch != '{' {
			return nil, false
		}
		end := strings.IndexByte(text[i+1:], '}')
		if end < 1 || end > 64 {
			return nil, false
		}
		name := text[i+1 : i+1+end]
		for _, r := range name {
			valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
			if !valid {
				return nil, false
			}
		}
		args[name] = true
		i += end + 1
	}
	return args, true
}

func copyPresentation(p *Presentation) *Presentation {
	if p == nil {
		return nil
	}
	value := &Presentation{Locale: p.Locale, Messages: map[string]string{}}
	for key, text := range browserCatalog()[p.Locale] {
		value.Messages[key] = text
	}
	for key, text := range p.Messages {
		value.Messages[key] = text
	}
	return value
}
