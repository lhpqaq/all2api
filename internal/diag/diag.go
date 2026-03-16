package diag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyDebug
)

func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, keyRequestID, id)
}

func RequestID(ctx context.Context) string {
	if v := ctx.Value(keyRequestID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func WithDebug(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, keyDebug, enabled)
}

func Debug(ctx context.Context) bool {
	if v := ctx.Value(keyDebug); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func NewRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err == nil {
		return "r_" + hex.EncodeToString(b)
	}
	return "r_" + hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
}
