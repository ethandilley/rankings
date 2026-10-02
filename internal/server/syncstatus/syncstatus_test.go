package syncstatus

import (
	"testing"
	"time"
)

func TestIntervalFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		enabled   bool
		interval  time.Duration
		wantError bool
	}{
		{
			name:    "empty disables the ticker",
			env:     "",
			enabled: false,
		},
		{
			name:    "whitespace disables the ticker",
			env:     "   ",
			enabled: false,
		},
		{
			name:     "parses minutes",
			env:      "30m",
			enabled:  true,
			interval: 30 * time.Minute,
		},
		{
			name:     "parses hours",
			env:      "6h",
			enabled:  true,
			interval: 6 * time.Hour,
		},
		{
			name:      "rejects non-duration values",
			env:       "every-hour",
			wantError: true,
		},
		{
			name:      "rejects zero",
			env:       "0s",
			wantError: true,
		},
		{
			name:      "rejects negative values",
			env:       "-1h",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ESPN_SYNC_INTERVAL", tt.env)

			enabled, interval, err := IntervalFromEnv()
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if enabled != tt.enabled {
				t.Fatalf("enabled = %v, want %v", enabled, tt.enabled)
			}
			if interval != tt.interval {
				t.Fatalf("interval = %v, want %v", interval, tt.interval)
			}
		})
	}
}
