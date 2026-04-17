package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type ctxKey int

const (
	ctxKeyTxID ctxKey = iota
)

// NewTransactionID returns a time-prefixed, 128-bit random identifier suitable
// for correlating audit records across a request boundary.
func NewTransactionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// WithTransactionID attaches txID to ctx.
func WithTransactionID(ctx context.Context, txID string) context.Context {
	return context.WithValue(ctx, ctxKeyTxID, txID)
}

// TransactionIDFromContext returns the txID from ctx, or empty if unset.
func TransactionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyTxID).(string)
	return v
}
