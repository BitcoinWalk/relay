package main

import (
	"context"
	"errors"
	"testing"
)

func TestChatRetryOnlyMetadataCollision(t *testing.T) {
	calls := 0
	if err := retryChatWrite(t.Context(), func() error {
		calls++
		if calls == 1 {
			return errors.New(metadataBudgetError)
		}
		return nil
	}); err != nil || calls != 2 {
		t.Fatal("metadata retry", calls, err)
	}
	calls = 0
	denied := errors.New("restricted: banned")
	if err := retryChatWrite(t.Context(), func() error { calls++; return denied }); err != denied || calls != 1 {
		t.Fatal("authorization retried")
	}
	calls = 0
	if err := retryChatWrite(t.Context(), func() error { calls++; return errors.New(metadataBudgetError) }); err == nil || calls != 2 {
		t.Fatal("retry must be bounded")
	}
}

func TestChatRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	err := retryChatWrite(ctx, func() error { calls++; return errors.New(metadataBudgetError) })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancelled request retried")
	}
}
