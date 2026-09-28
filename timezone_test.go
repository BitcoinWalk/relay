package main

import (
	"testing"
	"time"
)

func TestEmbeddedTimezoneDatabaseSupportsMemphis(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	winter := time.Date(2026, time.January, 10, 10, 0, 0, 0, location)
	summer := time.Date(2026, time.July, 10, 10, 0, 0, 0, location)
	_, winterOffset := winter.Zone()
	_, summerOffset := summer.Zone()
	if winterOffset != -6*60*60 || summerOffset != -5*60*60 {
		t.Fatalf("unexpected Memphis offsets: winter=%d summer=%d", winterOffset, summerOffset)
	}
}

func TestTimezoneProbeFailsClosed(t *testing.T) {
	t.Setenv("RELAY_TIMEZONE_PROBE", "Not/A_Real_Zone")
	if err := run(); err == nil {
		t.Fatal("timezone probe accepted an unknown zone")
	}
}
