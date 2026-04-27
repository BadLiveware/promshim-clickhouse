package obs

import (
	"context"
	"strings"
)

const maxLogCommentLength = 512

type logCommentCtxKey struct{}

// WithLogComment returns a context that carries the given bench/trace tag.
// The storage client reads this and propagates it to ClickHouse as a
// `log_comment` query parameter, landing in system.query_log.log_comment so
// profile captures can group/correlate queries across SQL-shape rewrites.
// Empty strings are ignored — no tag is attached.
func WithLogComment(ctx context.Context, tag string) context.Context {
	tag = normalizeLogComment(tag)
	if tag == "" {
		return ctx
	}
	return context.WithValue(ctx, logCommentCtxKey{}, tag)
}

// LogCommentFromContext returns the tag stored by WithLogComment, or "".
func LogCommentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(logCommentCtxKey{}).(string)
	return v
}

func normalizeLogComment(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	tag = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, tag)
	if len(tag) > maxLogCommentLength {
		return tag[:maxLogCommentLength]
	}
	return tag
}
