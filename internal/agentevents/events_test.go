package agentevents

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEmitWithoutSinkIsNoop(t *testing.T) {
	Emit(context.Background(), Event{Type: ToolStart}) // must not panic
	if SinkFrom(context.Background()) != nil {
		t.Fatal("background context has a sink")
	}
	if ctx := WithSink(context.Background(), nil); SinkFrom(ctx) != nil {
		t.Fatal("nil sink was stored")
	}
}

func TestEmitStampsTimeAndCapsOutput(t *testing.T) {
	var got []Event
	ctx := WithSink(context.Background(), func(e Event) { got = append(got, e) })
	Emit(ctx, Event{Type: ToolEnd, Output: strings.Repeat("é", MaxOutput)})
	if len(got) != 1 || got[0].At == 0 {
		t.Fatalf("event not delivered or not stamped: %+v", got)
	}
	if len(got[0].Output) > MaxOutput+len("…") || !utf8.ValidString(got[0].Output) {
		t.Errorf("output not capped on a rune boundary: %d bytes", len(got[0].Output))
	}
}
