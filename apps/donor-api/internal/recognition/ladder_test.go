package recognition

import (
	"testing"
	"time"
)

func TestStreakLadderDaysMilestonesAndBar(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// No run: zero days, first milestone ahead, empty 30-day bar.
	empty := Streak{}.Ladder(asOf)
	if empty.Days != 0 || empty.LastMilestone != 0 || empty.NextMilestone == nil || *empty.NextMilestone != 7 {
		t.Fatalf("empty ladder = %+v", empty)
	}
	if *empty.DaysToNext != 7 || len(empty.Bar) != 30 {
		t.Fatalf("empty ladder bar/next = %+v", empty)
	}
	for i, on := range empty.Bar {
		if on {
			t.Fatalf("empty bar day %d is on", i)
		}
	}

	cases := []struct {
		days         int64
		wantLast     int64
		wantNext     int64
		wantToNext   int64
		wantBarOnEnd int // how many trailing bar days should be on
	}{
		{days: 3, wantLast: 0, wantNext: 7, wantToNext: 4, wantBarOnEnd: 4},   // days>=0..3 -> 4 days on
		{days: 7, wantLast: 7, wantNext: 30, wantToNext: 23, wantBarOnEnd: 8}, // day 7 reached
		{days: 100, wantLast: 100, wantNext: 365, wantToNext: 265, wantBarOnEnd: 30},
		{days: 365, wantLast: 365, wantNext: 500, wantToNext: 135, wantBarOnEnd: 30},
		{days: 1460, wantLast: 1460, wantNext: 1460 + 365, wantToNext: 365, wantBarOnEnd: 30},
		{days: 1500, wantLast: 1460, wantNext: 1460 + 365, wantToNext: 1825 - 1500, wantBarOnEnd: 30},
	}
	for _, c := range cases {
		since := asOf.Add(-time.Duration(c.days) * 24 * time.Hour)
		l := Streak{Since: &since}.Ladder(asOf)
		if l.Days != c.days {
			t.Errorf("days=%d: got Days=%d", c.days, l.Days)
		}
		if l.LastMilestone != c.wantLast {
			t.Errorf("days=%d: LastMilestone=%d want %d", c.days, l.LastMilestone, c.wantLast)
		}
		if l.NextMilestone == nil || *l.NextMilestone != c.wantNext {
			t.Errorf("days=%d: NextMilestone=%v want %d", c.days, l.NextMilestone, c.wantNext)
		}
		if l.DaysToNext == nil || *l.DaysToNext != c.wantToNext {
			t.Errorf("days=%d: DaysToNext=%v want %d", c.days, l.DaysToNext, c.wantToNext)
		}
		on := 0
		for _, b := range l.Bar {
			if b {
				on++
			}
		}
		if on != c.wantBarOnEnd {
			t.Errorf("days=%d: bar on=%d want %d", c.days, on, c.wantBarOnEnd)
		}
	}
}
