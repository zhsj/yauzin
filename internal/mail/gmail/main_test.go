package gmail

import (
	"os"
	"testing"
)

// TestMain removes the backoff pause from the retry loop. Several tests answer
// with a retryable status on purpose, and the loop must not turn each of them
// into a seven second wait; retryDelay itself is measured in quota_test.go.
func TestMain(m *testing.M) {
	retryBaseDelay = 0
	os.Exit(m.Run())
}
