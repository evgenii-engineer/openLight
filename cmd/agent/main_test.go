package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsExpectedShutdown(t *testing.T) {
	t.Parallel()

	if !isExpectedShutdown(nil) {
		t.Fatal("expected nil error to be treated as clean shutdown")
	}

	if !isExpectedShutdown(context.Canceled) {
		t.Fatal("expected context.Canceled to be treated as clean shutdown")
	}

	if isExpectedShutdown(errors.New("wrap: " + context.Canceled.Error())) {
		t.Fatal("plain text error should not be treated as clean shutdown")
	}
}

func TestIsExpectedShutdownWrappedCanceled(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("poll failed: %w", context.Canceled)
	if !isExpectedShutdown(err) {
		t.Fatalf("expected wrapped context cancellation to be ignored, got %v", err)
	}
}

func TestIsExpectedShutdownRejectsOtherErrors(t *testing.T) {
	t.Parallel()

	if isExpectedShutdown(context.DeadlineExceeded) {
		t.Fatal("did not expect deadline exceeded to be treated as clean shutdown")
	}
}
