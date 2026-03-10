package main

import "testing"

func TestProgressStatusLine(t *testing.T) {
	prevSuccess := downloadSuccessCount.Load()
	prevErrors := downloadErrorCount.Load()
	defer func() {
		downloadSuccessCount.Store(prevSuccess)
		downloadErrorCount.Store(prevErrors)
	}()

	downloadSuccessCount.Store(3)
	downloadErrorCount.Store(1)

	tq := &TaskQueue{}
	tq.enqueuedTasks.Store(10)
	tq.runningTasks.Store(2)
	tq.completedTasks.Store(4)
	tq.activeDownloadTasks.Store(2)

	got := tq.progressStatusLine()
	want := "Progress: downloaded 3 | in progress 2 | queued 4 | errors 1"
	if got != want {
		t.Fatalf("unexpected progress line:\n got: %q\nwant: %q", got, want)
	}
}

func TestProgressStatusLineClampsQueuedToZero(t *testing.T) {
	prevSuccess := downloadSuccessCount.Load()
	prevErrors := downloadErrorCount.Load()
	defer func() {
		downloadSuccessCount.Store(prevSuccess)
		downloadErrorCount.Store(prevErrors)
	}()

	downloadSuccessCount.Store(0)
	downloadErrorCount.Store(2)

	tq := &TaskQueue{}
	tq.enqueuedTasks.Store(1)
	tq.runningTasks.Store(2)
	tq.completedTasks.Store(1)
	tq.activeDownloadTasks.Store(1)

	got := tq.progressStatusLine()
	want := "Progress: downloaded 0 | in progress 1 | queued 0 | errors 2"
	if got != want {
		t.Fatalf("unexpected progress line:\n got: %q\nwant: %q", got, want)
	}
}
