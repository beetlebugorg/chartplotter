package s52

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FindPresentationLibrary searches for S-52 presentation library (DAI file) in
// XDG-compliant locations.
//
// Search order:
//  1. $XDG_CONFIG_HOME/chartplotter/preslib.dai (or ~/.config/chartplotter/preslib.dai)
//  2. $XDG_DATA_HOME/chartplotter/preslib.dai (or ~/.local/share/chartplotter/preslib.dai)
//  3. /usr/local/share/chartplotter/preslib.dai (system-wide on Unix)
//  4. /usr/share/chartplotter/preslib.dai (system-wide on Linux)
//
// Returns the path to the first existing DAI file, or an error if none found.
//
// Example:
//
//	daiPath, err := s52.FindPresentationLibrary()
//	if err != nil {
//	    // No DAI file found - user must provide path
//	    log.Fatal("S-52 presentation library not found. Please specify --dai flag.")
//	}
//	lib, err := s52.LoadLibrary(daiPath)
func FindPresentationLibrary() (string, error) {
	locations := getPresentationLibraryLocations()

	// Check each location in order
	for _, path := range locations {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("S-52 presentation library not found in standard locations: %v", locations)
}

// getPresentationLibraryLocations returns the list of locations to search for
// the S-52 presentation library, in priority order.
func getPresentationLibraryLocations() []string {
	locations := []string{}
	home, _ := os.UserHomeDir()

	// 1. XDG_CONFIG_HOME (user configuration)
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		locations = append(locations, filepath.Join(xdgConfig, "chartplotter", "preslib.dai"))
	} else if home != "" {
		locations = append(locations, filepath.Join(home, ".config", "chartplotter", "preslib.dai"))
	}

	// 2. XDG_DATA_HOME (user data)
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		locations = append(locations, filepath.Join(xdgData, "chartplotter", "preslib.dai"))
	} else if home != "" {
		locations = append(locations, filepath.Join(home, ".local", "share", "chartplotter", "preslib.dai"))
	}

	// 3. System-wide locations (Unix)
	locations = append(locations,
		"/usr/local/share/chartplotter/preslib.dai",
		"/usr/share/chartplotter/preslib.dai",
	)

	return locations
}

// InstallPresentationLibrary copies an S-52 presentation library file to the
// user's XDG config directory, making it available for future use.
//
// This is a convenience function for first-time setup. The DAI file will be
// copied to $XDG_CONFIG_HOME/chartplotter/preslib.dai (or ~/.config/chartplotter/preslib.dai).
//
// Example:
//
//	// Copy downloaded DAI file to standard location
//	err := s52.InstallPresentationLibrary("/tmp/PresLib_e4.0.0.dai")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// Now FindPresentationLibrary() will find it automatically
func InstallPresentationLibrary(sourcePath string) error {
	// Get user config directory
	var configDir string
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		configDir = filepath.Join(xdgConfig, "chartplotter")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home directory: %w", err)
		}
		configDir = filepath.Join(home, ".config", "chartplotter")
	}

	// Create config directory
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	destPath := filepath.Join(configDir, "preslib.dai")

	// Copy file
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer source.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	return nil
}

// FindMarinerSettings searches for mariner settings YAML file in XDG-compliant locations.
//
// Search order:
//  1. $XDG_CONFIG_HOME/chartplotter/mariner.yaml (or ~/.config/chartplotter/mariner.yaml)
//  2. $XDG_DATA_HOME/chartplotter/mariner.yaml (or ~/.local/share/chartplotter/mariner.yaml)
//  3. /usr/local/share/chartplotter/mariner.yaml (system-wide on Unix)
//  4. /usr/share/chartplotter/mariner.yaml (system-wide on Linux)
//
// Returns the path to the first existing YAML file, or empty string if none found.
func FindMarinerSettings() string {
	locations := getMarinerSettingsLocations()

	// Check each location in order
	for _, path := range locations {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// getMarinerSettingsLocations returns the list of locations to search for
// mariner settings, in priority order.
func getMarinerSettingsLocations() []string {
	locations := []string{}
	home, _ := os.UserHomeDir()

	// 1. XDG_CONFIG_HOME (user configuration)
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		locations = append(locations, filepath.Join(xdgConfig, "chartplotter", "mariner.yaml"))
	} else if home != "" {
		locations = append(locations, filepath.Join(home, ".config", "chartplotter", "mariner.yaml"))
	}

	// 2. XDG_DATA_HOME (user data)
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		locations = append(locations, filepath.Join(xdgData, "chartplotter", "mariner.yaml"))
	} else if home != "" {
		locations = append(locations, filepath.Join(home, ".local", "share", "chartplotter", "mariner.yaml"))
	}

	// 3. System-wide locations (Unix)
	locations = append(locations,
		"/usr/local/share/chartplotter/mariner.yaml",
		"/usr/share/chartplotter/mariner.yaml",
	)

	return locations
}
