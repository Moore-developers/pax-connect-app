package historystore

import (
	"context"
	"database/sql"

	"pax-connect/daemon/internal/config"
)

type columnQueryer interface {
	queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (c *Columns) answers(ctx context.Context, kind int, key string, f *config.FlowFact) error {
	return columnAnswers(ctx, c.r, kind, key, f)
}
func columnAnswers(ctx context.Context, q columnQueryer, kind int, key string, f *config.FlowFact) error {
	if f.DNS == nil || f.DNS.Answers == nil {
		return nil
	}
	rows, err := q.QueryContext(ctx, "SELECT value FROM col_answers WHERE owner_kind=? AND owner_key=? ORDER BY ordinal", kind, key)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return err
		}
		f.DNS.Answers = append(f.DNS.Answers, s)
	}
	return rows.Err()
}
func (c *Columns) eventChildren(ctx context.Context, e *config.HistoryEvent) error {
	return columnEventChildren(ctx, c.r, e)
}
func columnEventChildren(ctx context.Context, q columnQueryer, e *config.HistoryEvent) error {
	if e.Flow != nil {
		if err := columnAnswers(ctx, q, 0, e.ID, e.Flow); err != nil {
			return err
		}
	}
	if e.Raw != nil {
		rows, err := q.QueryContext(ctx, "SELECT value FROM col_raw WHERE event_id=? ORDER BY ordinal", e.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s string
			if err = rows.Scan(&s); err != nil {
				_ = rows.Close()
				return err
			}
			e.Raw = append(e.Raw, s)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if e.System == nil || e.System.Sections == nil {
		return nil
	}
	rows, err := q.QueryContext(ctx, "SELECT kind,title,fields_present FROM col_sections WHERE event_id=? ORDER BY ordinal", e.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s config.EventSection
		var present bool
		if err = rows.Scan(&s.Kind, &s.Title, &present); err != nil {
			_ = rows.Close()
			return err
		}
		if present {
			s.Fields = []config.EventField{}
		}
		e.System.Sections = append(e.System.Sections, s)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for i := range e.System.Sections {
		s := &e.System.Sections[i]
		if s.Fields == nil {
			continue
		}
		fields, er := q.QueryContext(ctx, "SELECT label,value,before_value,after_value FROM col_fields WHERE event_id=? AND section_ordinal=? ORDER BY ordinal", e.ID, i)
		if er != nil {
			return er
		}
		for fields.Next() {
			var f config.EventField
			if er = fields.Scan(&f.Label, &f.Value, &f.Before, &f.After); er != nil {
				_ = fields.Close()
				return er
			}
			s.Fields = append(s.Fields, f)
		}
		er = fields.Err()
		closeErr = fields.Close()
		if er != nil {
			return er
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
