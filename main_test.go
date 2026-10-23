package bungkus

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	backoff = func(int) time.Duration { return 0 } // retries in tests should not wait
	os.Exit(m.Run())
}
