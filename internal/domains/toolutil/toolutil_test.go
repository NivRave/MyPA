package toolutil

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/events"
)

func TestToolUtil_Parsers(t *testing.T) {
	args := map[string]interface{}{
		"str":       "  hello world  ",
		"empty_str": "   ",
		"int_val":   42,
		"int64_val": int64(9999999999),
		"float_val": 3.1415,
		"bool_val":  "true",
		"bool_num":  1,
		"bool_no":   "no",
		"slice_str": []string{"a", "b", "c"},
		"slice_any": []interface{}{"x", "  y  ", 123},
		"time_val":  "2026-11-05T12:00:00Z",
		"date_val":  "2026-11-05",
	}

	if got := String(args, "str"); got != "hello world" {
		t.Errorf("String(str) = %q, want 'hello world'", got)
	}
	if got := String(args, "empty_str", "default"); got != "default" {
		t.Errorf("String(empty_str) = %q, want 'default'", got)
	}
	if got := String(args, "missing", "fallback"); got != "fallback" {
		t.Errorf("String(missing) = %q, want 'fallback'", got)
	}

	s1 := StringSlice(args, "slice_str")
	if len(s1) != 3 || s1[0] != "a" {
		t.Errorf("StringSlice(slice_str) = %v", s1)
	}
	s2 := StringSlice(args, "slice_any")
	if len(s2) != 2 || s2[0] != "x" || s2[1] != "y" {
		t.Errorf("StringSlice(slice_any) = %v", s2)
	}

	if got := Int(args, "int_val", 0); got != 42 {
		t.Errorf("Int(int_val) = %d, want 42", got)
	}
	if got := Int(args, "missing", 10); got != 10 {
		t.Errorf("Int(missing) = %d, want 10", got)
	}

	if got := Int64(args, "int64_val", 0); got != 9999999999 {
		t.Errorf("Int64(int64_val) = %d, want 9999999999", got)
	}

	if got := Uint(args, "int_val", 0); got != 42 {
		t.Errorf("Uint(int_val) = %d, want 42", got)
	}

	if got := Float64(args, "float_val", 0); got != 3.1415 {
		t.Errorf("Float64(float_val) = %f, want 3.1415", got)
	}

	if got := Bool(args, "bool_val", false); !got {
		t.Errorf("Bool(bool_val) = false, want true")
	}
	if got := Bool(args, "bool_num", false); !got {
		t.Errorf("Bool(bool_num) = false, want true")
	}
	if got := Bool(args, "bool_no", true); got {
		t.Errorf("Bool(bool_no) = true, want false")
	}

	tm, err := ParseTime(String(args, "time_val"))
	if err != nil || tm.Year() != 2026 {
		t.Errorf("ParseTime(time_val) err = %v, tm = %v", err, tm)
	}

	d, err := ParseTime(String(args, "date_val"))
	if err != nil || d.Month() != 11 {
		t.Errorf("ParseTime(date_val) err = %v, tm = %v", err, d)
	}

	tPtr, err := Time(args, "time_val")
	if err != nil || tPtr == nil || tPtr.Day() != 5 {
		t.Errorf("Time(time_val) err = %v, tPtr = %v", err, tPtr)
	}

	jsonStr := FormatJSON(map[string]string{"foo": "bar"})
	if jsonStr != `{"foo":"bar"}` {
		t.Errorf("FormatJSON = %s", jsonStr)
	}
}

func TestToolUtil_PublishMutation(t *testing.T) {
	bus := events.NewEventBus()
	ch := make(chan events.MutationEvent, 1)
	bus.SubscribeTopic("test", func(ctx context.Context, ev events.MutationEvent) error {
		ch <- ev
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := PublishMutation(ctx, bus, events.MutationEvent{
		Topic:       "test",
		Action:      "create",
		EntityType:  "Item",
		EntityID:    "123",
		ActorUserID: "user1",
		Summary:     "Created item",
	})
	if err != nil {
		t.Fatalf("PublishMutation failed: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.EntityID != "123" || ev.ID == "" || ev.Timestamp.IsZero() {
			t.Errorf("Unexpected event received: %+v", ev)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    uint
		wantErr bool
	}{
		{name: "valid positive", input: "42", want: 42, wantErr: false},
		{name: "valid with whitespace", input: "  100  ", want: 100, wantErr: false},
		{name: "empty string", input: "", want: 0, wantErr: true},
		{name: "whitespace only", input: "   ", want: 0, wantErr: true},
		{name: "zero", input: "0", want: 0, wantErr: true},
		{name: "negative", input: "-5", want: 0, wantErr: true},
		{name: "non-numeric", input: "abc", want: 0, wantErr: true},
		{name: "float string", input: "3.14", want: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

