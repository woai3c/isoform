package main

// handoffCreationResult mirrors automationCreationResult — Status is one of
// "created" | "skipped" | "error", and ProposalID is set when Status="created".
type handoffCreationResult struct {
	Status     string
	ProposalID string
	Reason     string
}
