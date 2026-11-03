//go:build !race

package main

// Preserve the real-size cold-read and truncation coverage without race overhead.
const topicRegistryFixtureSessions = 3000

const topicColdFixtureSessions = 18000

const topicColdProofBudgetMillis = 100
