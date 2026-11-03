package main

import "wails-gui/internal/remoteaccess"

func LocateBundledDumbpipe() (string, error) {
	return remoteaccess.LocateBundledBinary(
		"ISOFORM_DUMBPIPE_BIN",
		"dumbpipe-bundle",
		"dumbpipe",
		remoteaccess.DevelopmentBinaryName("dumbpipe"),
		"dumbpipe",
	)
}

func LocateBundledRelay() (string, error) {
	return remoteaccess.LocateBundledBinary(
		"ISOFORM_RELAY_BIN",
		"relay-bundle",
		"isoform-relay",
		remoteaccess.DevelopmentBinaryName("isoform-relay"),
		"isoform-relay",
	)
}
