// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package infrafiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"

	"github.com/podplane/podplane/internal/tui"
)

// ConfirmConfigPath checks whether create would introduce files into a
// non-empty config directory and asks the user where to generate them.
func ConfirmConfigPath(path string, originDir string, fileSet string, defaultDirName string) (string, error) {
	return confirmNewConfigDirectoryAt(path, originDir, fileSet, defaultDirName)
}

func confirmNewConfigDirectoryAt(path string, originDir string, fileSet string, defaultDirName string) (string, error) {
	dir := filepath.Dir(path)
	empty, err := directoryEmpty(dir)
	if err != nil {
		return "", err
	}
	if empty {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create output directory %s: %w", dir, err)
		}
		return path, nil
	}
	fmt.Printf("Podplane will generate %s files, but %s is not empty.\n", fileSet, dir)
	choice, quitting, err := tui.SelectList("Choose where to generate files", "Target directory is not empty", []list.Item{
		tui.Item{Key: "directory", Label: "Specify a directory"},
		tui.Item{Key: "current", Label: "Continue in this directory"},
		tui.Item{Label: "Cancel", Cancel: true},
	})
	if err != nil {
		return "", err
	}
	if quitting {
		return "", fmt.Errorf("creation cancelled")
	}
	switch choice {
	case "current":
		return path, nil
	case "directory":
		name, err := tui.Input("Output directory", "Directory path", defaultDirName, validateOutputDirectory(originDir))
		if err != nil {
			return "", err
		}
		newDir := resolveOutputDirectory(originDir, name)
		if err := os.MkdirAll(newDir, 0o755); err != nil {
			return "", fmt.Errorf("create output directory %s: %w", newDir, err)
		}
		return confirmNewConfigDirectoryAt(filepath.Join(newDir, filepath.Base(path)), originDir, fileSet, defaultDirName)
	default:
		return "", fmt.Errorf("creation cancelled")
	}
}

// directoryEmpty reports whether dir exists and contains no entries.
func directoryEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read directory %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}

func validateOutputDirectory(originDir string) tui.InputValidator {
	return func(value string) error {
		name := strings.TrimSpace(value)
		if name == "" {
			return fmt.Errorf("directory path is required")
		}
		path := resolveOutputDirectory(originDir, name)
		_, err := os.ReadDir(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read directory %s: %w", path, err)
		}
		return nil
	}
}

func resolveOutputDirectory(originDir string, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(originDir, path)
}
