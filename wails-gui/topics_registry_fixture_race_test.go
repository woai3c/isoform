//go:build race

package main

// Bound the session workload under race instrumentation; production is unchanged.
const topicRegistryFixtureSessions = 256

const topicColdFixtureSessions = topicRegistryFixtureSessions

const topicColdProofBudgetMillis = 1000
