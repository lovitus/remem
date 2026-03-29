package guard

import (
	"testing"
	"time"
)

func TestPerfCaptureWindowExpires(t *testing.T) {
	var m Monitor
	until := m.StartPerfCapture(20 * time.Millisecond)
	if until.IsZero() {
		t.Fatalf("expected capture window end time")
	}
	if !m.perfCaptureActive(time.Now()) {
		t.Fatalf("expected capture to be active immediately")
	}
	if m.perfCaptureActive(until.Add(5 * time.Millisecond)) {
		t.Fatalf("expected capture to expire after window")
	}
	if m.perfCaptureActive(time.Now().Add(50 * time.Millisecond)) {
		t.Fatalf("expected expired capture to stay disabled")
	}
}
