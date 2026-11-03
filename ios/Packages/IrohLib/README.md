# Vendored iroh-ffi

This package is vendored from n0-computer/iroh-ffi tag v1.2.0
(commit 66a8d619a4d791d458d582f312df6e0ab42bc541).

The only change to the upstream package is its iOS platform line, from
17.5 to 17.0. Upstream raised it in commit 6ceb0d0 citing
`nw_path_is_ultra_constrained`; the v1.2.0 binary references no Network API
newer than iOS 13 and does not reference that symbol. This lets the app keep
iOS 17.0.

The binary target still downloads the upstream release zip, pinned by its
checksum in `Package.swift`.

To update this vendor, copy the new upstream files and re-apply the iOS
platform-line change.
