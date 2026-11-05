package main

import (
	"log"
	"reflect"
	"runtime"
	"strings"
	"time"
)

// Only binding names, elapsed time and cardinality belong in performance logs.
// Never log arguments, queries, titles, identifiers, content or errors here.
func logPageBinding(name string, started time.Time, result any) {
	log.Printf("[perf] binding=%s ms=%d rows=%d origin=%s", name, time.Since(started).Milliseconds(), pageBindingRows(reflect.ValueOf(result)), bindingOrigin())
}

// bindingOrigin says whether a binding ran for a web view through the Wails
// bridge ("window") or for Go code that called the method directly ("internal").
// Locked-window call counts count only "window". The check reads the caller
// stack: the bridge invokes bound methods through internal/binding.BoundMethod.Call.
func bindingOrigin() string {
	pcs := make([]uintptr, 48)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if isBridgeFrame(frame.Function) {
			return "window"
		}
		if !more {
			return "internal"
		}
	}
}

// isBridgeFrame names the Wails frame that dispatches a bound method call.
func isBridgeFrame(function string) bool {
	return strings.Contains(function, "internal/binding.(*BoundMethod).Call")
}

func pageBindingRows(value reflect.Value) int {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return value.Len()
	case reflect.Struct:
		// Count primary rows only; supplementary metadata is not another row.
		for _, field := range []string{"Items", "Beliefs", "Threads", "Kinds"} {
			if rows := value.FieldByName(field); rows.IsValid() && rows.Kind() == reflect.Slice {
				return rows.Len()
			}
		}
		if field := value.FieldByName("KnownCount"); field.IsValid() {
			return int(field.Int())
		}
		if field := value.FieldByName("Tracking"); field.IsValid() {
			return field.Len() + value.FieldByName("Done").Len() + value.FieldByName("Held").Len()
		}
		if groups := value.FieldByName("Groups"); groups.IsValid() && groups.Kind() == reflect.Slice {
			rows := 0
			for i := 0; i < groups.Len(); i++ {
				rows += groups.Index(i).FieldByName("Results").Len()
			}
			return rows
		}
		if field := value.FieldByName("Conversations"); field.IsValid() {
			return int(field.Int() + value.FieldByName("Waiting").Int() + value.FieldByName("Finished").Int() + value.FieldByName("Routine").Int())
		}
	}
	return 1
}
