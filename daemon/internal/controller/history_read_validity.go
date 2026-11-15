package controller

import (
	"context"
	"errors"
)

func (cache *readTokenCache) ensureLive(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	r := cache.reads[token]
	if r == nil || r.snapshotRevoked() {
		return errors.New("history_cursor_expired")
	}
	return nil
}
