package registry

import "context"

// userIDKey carries the acting user id on the context so tool executors (whose
// signature has no user id) can still act as that user for authorization, RLS
// and auditing.
type userIDKey struct{}

// WithUserID attaches the acting user id to a context.
func WithUserID(ctx context.Context, userID int) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

// UserIDFrom returns the acting user id attached by WithUserID, or 0.
func UserIDFrom(ctx context.Context) int {
	if v, ok := ctx.Value(userIDKey{}).(int); ok {
		return v
	}
	return 0
}
