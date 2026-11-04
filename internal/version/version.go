// Package version exposes the build and protocol identity injected at release time.
package version

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"os"
)

// Product is replaced with semantic release version through -ldflags.
var Product = "0.0.0-dev"

// BuildID returns a stable identity for one executable's exact contents.
func BuildID() string {
	executable, err := os.Executable()
	if err == nil {
		if data, readErr := os.ReadFile(executable); readErr == nil {
			digest := sha256.Sum256(data)
			return hex.EncodeToString(digest[:12])
		}
	}
	if info, ok := buildinfo.ReadFile(os.Args[0]); ok == nil && info != nil {
		digest := sha256.Sum256([]byte(info.String()))
		return hex.EncodeToString(digest[:12])
	}
	return "unknown"
}
