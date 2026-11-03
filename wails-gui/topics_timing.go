package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

func timeTopicStep(scope, step string) func(...any) {
	started := time.Now()
	var once sync.Once
	return func(fields ...any) {
		once.Do(func() {
			var counts strings.Builder
			for i := 0; i+1 < len(fields); i += 2 {
				fmt.Fprintf(&counts, " %v=%v", fields[i], fields[i+1])
			}
			log.Printf("[topics] %s level=info step=%s elapsed_ms=%d%s", scope, step, time.Since(started).Milliseconds(), counts.String())
		})
	}
}
