package logging

import "testing"

func TestMessageWithName(t *testing.T) {
	l := Named("unit")
	got := l.messageWithName("hello")
	if got != "[unit] hello" {
		t.Fatalf("unexpected named message: %q", got)
	}
}

func TestMessageWithNameNilLogger(t *testing.T) {
	var l *Logger
	got := l.messageWithName("hello")
	if got != "hello" {
		t.Fatalf("unexpected nil-logger message: %q", got)
	}
}

func TestGetReturnsNamedLogger(t *testing.T) {
	l := Get("mod")
	if l == nil {
		t.Fatalf("expected non-nil logger")
	}
	if l.name != "mod" {
		t.Fatalf("expected logger name %q, got %q", "mod", l.name)
	}
}
