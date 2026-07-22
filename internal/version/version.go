// Package version carries the build stamp.
package version

// Version is overwritten at build time with the manifest's version, so a
// source build reports the same thing `herdr plugin list` does.
var Version = "dev"
