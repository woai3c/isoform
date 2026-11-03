//go:build !darwin

package main

func readObservationAppCategory(_ SystemApp) string { return "" }
