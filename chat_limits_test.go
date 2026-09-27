package main

import (
	"fmt"
	"testing"
	"time"
)

func TestChatLimitsBoundAndReset(t *testing.T) {
	l := &chatLimits{}
	now := time.Now()
	for range 3 {
		if !l.allow("key", 3, time.Minute, now) {
			t.Fatal("early limit")
		}
	}
	if l.allow("key", 3, time.Minute, now) {
		t.Fatal("limit bypass")
	}
	if !l.allow("key", 3, time.Minute, now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
	l = &chatLimits{}
	for i := 0; i < 4096; i++ {
		if !l.allow(fmt.Sprint(i), 1, time.Minute, now) {
			t.Fatal("early capacity limit")
		}
	}
	if l.allow("overflow", 1, time.Minute, now) {
		t.Fatal("unbounded identities")
	}
	if !l.allow("new", 1, time.Minute, now.Add(time.Minute)) {
		t.Fatal("expired identities not reclaimed")
	}
}
