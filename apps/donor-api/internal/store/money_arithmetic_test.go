package store

import (
	"math"
	"testing"
)

func TestMatchOwedExactArithmetic(t *testing.T) {
	for _, tt := range []struct {
		name      string
		gifts     int64
		rate      int32
		cap       int64
		want      int64
		wantError bool
	}{
		{"fractional cent rounds down", 101, 5000, 0, 50, false},
		{"large intermediate product", math.MaxInt64, 10000, 0, math.MaxInt64, false},
		{"cap applied after exact product", math.MaxInt64, math.MaxInt32, 5000, 5000, false},
		{"uncapped overflow refused", math.MaxInt64, 10001, 0, 0, true},
		{"negative gifts refused", -1, 10000, 0, 0, true},
		{"negative rate refused", 100, -1, 0, 0, true},
		{"negative cap refused", 100, 10000, -1, 0, true},
		{"zero rate", math.MaxInt64, 0, 0, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchOwed(tt.gifts, tt.rate, tt.cap)
			if (err != nil) != tt.wantError || got != tt.want {
				t.Fatalf("matchOwed = %d, %v; want %d, error=%t", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestPercentExactArithmetic(t *testing.T) {
	for _, tt := range []struct {
		part, whole int64
		want        int
	}{
		{math.MaxInt64 - 1, math.MaxInt64, 99},
		{math.MaxInt64 / 2, math.MaxInt64, 49},
		{math.MaxInt64, 1, 100},
		{1, 3, 33},
		{1, 0, 0},
		{-1, 100, 0},
	} {
		if got := percent(tt.part, tt.whole); got != tt.want {
			t.Errorf("percent(%d, %d) = %d; want %d", tt.part, tt.whole, got, tt.want)
		}
	}
}
