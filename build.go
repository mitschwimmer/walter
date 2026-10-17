package main

import (
	"runtime/debug"
	"strings"
)

// Release builds set revision through -ldflags "-X main.revision=<git SHA>".
var revision string

func buildRevision() string {
	ref := revision
	if ref == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			dirty := false
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					ref = setting.Value
				case "vcs.modified":
					dirty = setting.Value == "true"
				}
			}
			if dirty && ref != "" {
				ref += "-dirty"
			}
		}
	}
	return shortRevision(ref)
}

func shortRevision(ref string) string {
	dirty := strings.HasSuffix(ref, "-dirty")
	ref = strings.TrimSuffix(ref, "-dirty")
	if ref == "" || ref == "unknown" {
		return "dev"
	}
	if len(ref) > 7 {
		ref = ref[:7]
	}
	if dirty {
		ref += "-dirty"
	}
	return ref
}
