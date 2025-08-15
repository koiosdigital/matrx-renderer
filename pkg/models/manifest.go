package models

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AppManifest represents the manifest.yaml structure for an app
type AppManifest struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Summary     string `yaml:"summary" json:"summary"`
	Description string `yaml:"desc" json:"description"`
	Author      string `yaml:"author" json:"author"`
	FileName    string `yaml:"fileName" json:"fileName"`
	PackageName string `yaml:"packageName" json:"packageName"`

	// Runtime fields (not in manifest)
	DirectoryPath string `yaml:"-" json:"directoryPath"`
	StarFilePath  string `yaml:"-" json:"starFilePath"`
}

// LoadManifest loads a manifest.yaml file from the given directory
func LoadManifest(appDir string) (*AppManifest, error) {
	manifestPath := filepath.Join(appDir, "manifest.yaml")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	var manifest AppManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest file: %w", err)
	}

	// Set runtime fields
	manifest.DirectoryPath = appDir
	manifest.StarFilePath = filepath.Join(appDir, manifest.FileName)

	// Validate that the star file exists
	if _, err := os.Stat(manifest.StarFilePath); err != nil {
		return nil, fmt.Errorf("star file not found: %s", manifest.StarFilePath)
	}

	return &manifest, nil
}

// AppRegistry manages the collection of available apps
type AppRegistry struct {
	apps map[string]*AppManifest
}

// NewAppRegistry creates a new app registry
func NewAppRegistry() *AppRegistry {
	return &AppRegistry{
		apps: make(map[string]*AppManifest),
	}
}

// LoadApps scans the apps directory and loads all app manifests
func (r *AppRegistry) LoadApps(appsDir string) error {
	// Clear existing apps
	r.apps = make(map[string]*AppManifest)

	// Structure: /opt/apps/apps/{app_id}/manifest.yaml
	fmt.Printf("DEBUG: Loading apps from directory: %s\n", appsDir)

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read apps directory: %w", err)
	}

	fmt.Printf("DEBUG: Found %d entries in apps directory\n", len(entries))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		appDir := filepath.Join(appsDir, entry.Name())
		fmt.Printf("DEBUG: Processing app directory: %s\n", appDir)

		manifest, err := LoadManifest(appDir)
		if err != nil {
			// Log error but continue loading other apps
			fmt.Printf("DEBUG: Failed to load manifest for %s: %v\n", entry.Name(), err)
			continue
		}

		fmt.Printf("DEBUG: Successfully loaded app: %s\n", manifest.ID)
		r.apps[manifest.ID] = manifest
	}

	fmt.Printf("DEBUG: Total apps loaded: %d\n", len(r.apps))
	return nil
}

// GetApp returns an app by ID
func (r *AppRegistry) GetApp(id string) (*AppManifest, bool) {
	app, exists := r.apps[id]
	return app, exists
}

// GetAllApps returns all loaded apps
func (r *AppRegistry) GetAllApps() map[string]*AppManifest {
	// Return a copy to prevent external modification
	result := make(map[string]*AppManifest)
	for k, v := range r.apps {
		result[k] = v
	}
	return result
}

// GetAppsList returns a list of all app manifests
func (r *AppRegistry) GetAppsList() []*AppManifest {
	apps := make([]*AppManifest, 0, len(r.apps))
	for _, app := range r.apps {
		apps = append(apps, app)
	}
	return apps
}
