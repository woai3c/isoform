package main

import "sync"

func readPermissionGrants(checks map[string]func() bool) map[string]bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := make(map[string]bool, len(checks))
	for key, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := check()
			mu.Lock()
			granted[key] = value
			mu.Unlock()
		}()
	}
	wg.Wait()
	return granted
}
