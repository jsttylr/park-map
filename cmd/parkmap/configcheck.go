package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var anonVolume = regexp.MustCompile(`volumes/[0-9a-f]{64}/_data`)

// configWarning says when, inside Docker, the config folder isn't mapped to a
// folder on the host, so settings would be lost when the container is updated.
func configWarning(dir string) string {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return ""
	}
	abs, _ := filepath.Abs(dir)
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[4] != abs {
			continue
		}
		if anonVolume.MatchString(f[3]) {
			return abs + " is not mapped to a folder on your server, so these settings will be lost when the container is updated. Add a path mapping for " + abs + " (on Unraid: /mnt/user/appdata/park-map), then save the settings again."
		}
		return ""
	}
	return abs + " is not mapped to a folder on your server, so these settings will be lost when the container is updated. Add a path mapping for " + abs + " (on Unraid: /mnt/user/appdata/park-map), then save the settings again."
}
