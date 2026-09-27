package main

import (
	"context"
	"time"
)

const metadataBudgetError = "rate-limited: metadata revision budget; retry next second"

// Only this specific pre-commit metadata collision is retryable. The store
// rolls back the entire transaction before returning it. Never retry arbitrary
// storage errors or rejected authorization, and never acknowledge before commit.
func retryChatWrite(ctx context.Context, apply func() error) error {
	err := apply()
	if err == nil || err.Error() != metadataBudgetError {
		return err
	}
	now := time.Now()
	delay := now.Truncate(time.Second).Add(time.Second + 10*time.Millisecond).Sub(now)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return apply()
}
