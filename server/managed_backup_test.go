package main

import (
	"testing"
	"time"
)

func TestBackupSlotUsesServerCalendar(t *testing.T) {
	zone := time.FixedZone("test-server", 3*60*60)
	now := time.Date(2026, time.November, 23, 18, 30, 0, 0, zone)
	settings := backupSettings{Days: []time.Weekday{time.Monday, time.Friday}, Time: "18:30"}
	if got := backupSlot(settings, now, false); !got.Equal(now) {
		t.Fatalf("current scheduled slot = %v, want %v", got, now)
	}
	if got, want := backupSlot(settings, now, true), now.AddDate(0, 0, 4); !got.Equal(want) {
		t.Fatalf("next scheduled slot = %v, want %v", got, want)
	}
}

func TestBackupScheduleRejectsInvalidTimeAndDays(t *testing.T) {
	for _, settings := range []backupSettings{
		{Time: "24:00", Days: []time.Weekday{time.Monday}},
		{Time: "03:00", Days: []time.Weekday{time.Monday, time.Monday}},
		{Time: "03:00", Days: []time.Weekday{8}},
		{Time: "03:00"},
	} {
		if err := validateBackupSchedule(settings); err == nil {
			t.Fatalf("accepted invalid schedule %+v", settings)
		}
	}
}
