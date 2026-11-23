package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// resetName is the name that resetState gives to the old state file:
// path.<UTC time>.reset. The name does not end in ".bak", so pruneBackups
// never removes it.
func resetName(path string, now time.Time) string {
	stamp := now.UTC().Format("20060102T150405Z")
	name := fmt.Sprintf("%s.%s.reset", path, stamp)
	for i := 2; fileExists(name); i++ {
		name = fmt.Sprintf("%s.%s.reset-%d", path, stamp, i)
	}
	return name
}

// resetState implements ResetState.
func resetState(cfg Config, now time.Time) (string, error) {
	if cfg.StatePath == "" {
		return "", errors.New("no state file")
	}
	lock, err := lockState(cfg.StatePath)
	if err != nil {
		return "", err
	}
	defer unlockState(lock)
	if !fileExists(cfg.StatePath) {
		return "", fmt.Errorf("%w: %s", fs.ErrNotExist, cfg.StatePath)
	}
	dst := resetName(cfg.StatePath, now)
	if err := os.Rename(cfg.StatePath, dst); err != nil {
		return "", err
	}
	return dst, nil
}
