package historystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"pax-connect/daemon/internal/config"
)

// A separate immutable child row adds meaning without rewriting existing column facts.
type eventTexts struct {
	Title, Name, Category *config.ProductText `json:",omitempty"`
	Sections              []sectionTexts      `json:",omitempty"`
}
type sectionTexts struct {
	Title  *config.ProductText `json:",omitempty"`
	Fields []fieldTexts        `json:",omitempty"`
}
type fieldTexts struct {
	Label, Value, Before, After *config.ProductText `json:",omitempty"`
}

func (t *ColumnsTx) putEventTexts(ctx context.Context, e config.HistoryEvent) error {
	v := eventTexts{Title: e.TitleText}
	if err := config.ValidateProductText(e.TitleText); err != nil {
		return err
	}
	if s := e.System; s != nil {
		v.Name, v.Category = s.NameText, s.Category.NameText
		for _, section := range s.Sections {
			block := sectionTexts{Title: section.TitleText}
			for _, field := range section.Fields {
				block.Fields = append(block.Fields, fieldTexts{field.LabelText, field.ValueText, field.BeforeText, field.AfterText})
			}
			v.Sections = append(v.Sections, block)
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// No separate row is needed for legacy events without semantic additions.
	if string(raw) == "{}" || !hasEventTexts(v) {
		return nil
	}
	return t.exec(ctx, "INSERT INTO col_texts(event_id,body) VALUES(?,?)", e.ID, raw)
}

func hasEventTexts(v eventTexts) bool {
	if v.Title != nil || v.Name != nil || v.Category != nil {
		return true
	}
	for _, section := range v.Sections {
		if section.Title != nil {
			return true
		}
		for _, field := range section.Fields {
			if field.Label != nil || field.Value != nil || field.Before != nil || field.After != nil {
				return true
			}
		}
	}
	return false
}

func columnEventTexts(ctx context.Context, q columnQueryer, e *config.HistoryEvent) error {
	var raw []byte
	err := q.QueryRowContext(ctx, "SELECT body FROM col_texts WHERE event_id=?", e.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var v eventTexts
	if err = json.Unmarshal(raw, &v); err != nil {
		return err
	}
	e.TitleText = v.Title
	if s := e.System; s != nil {
		s.NameText, s.Category.NameText = v.Name, v.Category
		if len(v.Sections) != len(s.Sections) {
			return errors.New("history_text_sections_mismatch")
		}
		for i, section := range v.Sections {
			block := &s.Sections[i]
			block.TitleText = section.Title
			if len(section.Fields) != len(block.Fields) {
				return errors.New("history_text_fields_mismatch")
			}
			for j, field := range section.Fields {
				f := &block.Fields[j]
				f.LabelText, f.ValueText, f.BeforeText, f.AfterText = field.Label, field.Value, field.Before, field.After
			}
		}
	}
	return nil
}
