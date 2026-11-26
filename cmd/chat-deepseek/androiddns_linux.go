//go:build linux || android

package main

// Import for its side effect: on Android (and on static Linux binaries
// running under Android kernels) this installs a dnsproxyd-backed
// resolver, because pure-Go binaries have no /etc/resolv.conf there.
import _ "tailscale.com/feature/androiddns"
