package calendar

import (
	"testing"
	"time"
)

func TestFreeBlocks(t *testing.T) {
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	at := func(hour int) time.Time { return day.Add(time.Duration(hour) * time.Hour) }
	events := []Event{
		{Start: at(10), End: at(12)},
		{Start: at(11), End: at(13)},
		{Start: at(14), End: at(15), Availability: "free"},
		{Start: at(16), End: at(17), MyStatus: StatusDeclined},
	}
	blocks, err := FreeBlocks(events, at(9), at(18), 30*time.Minute)
	if err != nil || len(blocks) != 2 || !blocks[0].Start.Equal(at(9)) || !blocks[0].End.Equal(at(10)) ||
		!blocks[1].Start.Equal(at(13)) || !blocks[1].End.Equal(at(18)) {
		t.Fatalf("blocks=%+v err=%v", blocks, err)
	}
	allDay := Event{Start: day, End: day.AddDate(0, 0, 1), AllDay: true}
	blocks, err = FreeBlocks([]Event{allDay}, at(9), at(18), 30*time.Minute)
	if err != nil || len(blocks) != 0 {
		t.Fatalf("busy all-day event left free time: %+v %v", blocks, err)
	}
	if _, err := FreeBlocks([]Event{{Start: at(10)}}, at(9), at(18), 30*time.Minute); err == nil {
		t.Fatal("invalid busy event must not imply free time")
	}
}
