package nixstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/nixstore"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"1GB", 1 << 30, false},
		{"1gb", 1 << 30, false},
		{"2GB", 2 << 30, false},
		{"500MB", 500 << 20, false},
		{"500mb", 500 << 20, false},
		{"1024KB", 1024 << 10, false},
		{"1024kb", 1024 << 10, false},
		{"0", 0, false},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := nixstore.ParseSize(tt.input)
			if tt.err {
				if err == nil {
					t.Errorf("ParseSize(%q) = %d, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSize(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestWithRetry_SucceedsImmediately(t *testing.T) {
	old := nixstore.RetryBaseDelay
	nixstore.RetryBaseDelay = time.Millisecond
	defer func() { nixstore.RetryBaseDelay = old }()

	calls := 0
	err := nixstore.WithRetry(context.Background(), 3, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestWithRetry_SucceedsAfterFailures(t *testing.T) {
	old := nixstore.RetryBaseDelay
	nixstore.RetryBaseDelay = time.Millisecond
	defer func() { nixstore.RetryBaseDelay = old }()

	calls := 0
	err := nixstore.WithRetry(context.Background(), 3, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetry_ExhaustsRetries(t *testing.T) {
	old := nixstore.RetryBaseDelay
	nixstore.RetryBaseDelay = time.Millisecond
	defer func() { nixstore.RetryBaseDelay = old }()

	calls := 0
	err := nixstore.WithRetry(context.Background(), 3, func() error {
		calls++
		return errors.New("permanent")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetry_ZeroMeansOnce(t *testing.T) {
	old := nixstore.RetryBaseDelay
	nixstore.RetryBaseDelay = time.Millisecond
	defer func() { nixstore.RetryBaseDelay = old }()

	calls := 0
	err := nixstore.WithRetry(context.Background(), 0, func() error {
		calls++
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestWithRetry_RespectsContextCancellation(t *testing.T) {
	old := nixstore.RetryBaseDelay
	nixstore.RetryBaseDelay = time.Millisecond
	defer func() { nixstore.RetryBaseDelay = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := nixstore.WithRetry(ctx, 5, func() error {
		calls++
		cancel()
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
