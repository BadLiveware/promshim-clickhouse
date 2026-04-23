package obs

import "context"

type logCommentCtxKey struct{}

// WithLogComment returns a context that carries the given bench/trace tag.
// The storage client reads this and propagates it to ClickHouse as a
// `log_comment` query parameter, landing in system.query_log.log_comment so
// profile captures can group/correlate queries across SQL-shape rewrites.
// Empty strings are ignored — no tag is attached.
func WithLogComment(ctx context.Context, tag string) context.Context {
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
