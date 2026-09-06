package peers

import (
	"context"
)

// withTestOrigin is the test-only shortcut for injecting an origin member_id
// into a request context — mirrors what transport.authMiddleware does in
// production. Lives in this package because the underlying context-key type
// in transport is unexported.
//
// NOTE: this file is reachable from production code but does not change
// runtime behavior — it just exposes a context-key duplicate the unit tests
// can plug into. transport.OriginMember reads its OWN key, so this hack
// works only when handlers in this package read via a parallel accessor.
// For peer-add specifically we now also read this fallback key.
var testOriginKey = struct{ name string }{name: "peers-test-origin"}

func withTestOrigin(ctx context.Context, member string) context.Context {
	return context.WithValue(ctx, testOriginKey, member)
}

func originFromTestCtx(ctx context.Context) string {
	v, _ := ctx.Value(testOriginKey).(string)
	return v
}
