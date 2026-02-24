package models

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestManifest(t *testing.T, dir, id, fileName string) {
	t.Helper()
	content := "id: " + id + "\nname: " + id + "\nsummary: test\ndesc: test\nauthor: test\nfileName: " + fileName + "\npackageName: apps." + id + "\n"
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}
}

func TestLoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	writeTestManifest(t, dir, "my-app", "my-app.star")
	os.WriteFile(filepath.Join(dir, "my-app.star"), []byte("# app"), 0644)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != "my-app" {
		t.Errorf("ID = %q, want my-app", m.ID)
	}
	if m.DirectoryPath != dir {
		t.Errorf("DirectoryPath = %q, want %q", m.DirectoryPath, dir)
	}
	expected := filepath.Join(dir, "my-app.star")
	if m.StarFilePath != expected {
		t.Errorf("StarFilePath = %q, want %q", m.StarFilePath, expected)
	}
}

func TestLoadManifest_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(dir)
	if err == nil {
		t.Error("expected error for missing manifest.yaml")
	}
}

func TestLoadManifest_MissingStarFile(t *testing.T) {
	dir := t.TempDir()
	writeTestManifest(t, dir, "my-app", "my-app.star")
	// Don't create the .star file

	_, err := LoadManifest(dir)
	if err == nil {
		t.Error("expected error for missing star file")
	}
}

func TestLoadManifest_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(": : bad yaml [[["), 0644)

	_, err := LoadManifest(dir)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// --- AppRegistry ---

func TestAppRegistry_GetApp(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0755)
	writeTestManifest(t, appDir, "app1", "app1.star")
	os.WriteFile(filepath.Join(appDir, "app1.star"), []byte("# app"), 0644)

	reg := NewAppRegistry()
	if err := reg.LoadApps(dir); err != nil {
		t.Fatalf("LoadApps: %v", err)
	}

	app, ok := reg.GetApp("app1")
	if !ok {
		t.Fatal("expected app1 to exist")
	}
	if app.ID != "app1" {
		t.Errorf("ID = %q, want app1", app.ID)
	}

	_, ok = reg.GetApp("nonexistent")
	if ok {
		t.Error("expected nonexistent to not exist")
	}
}

func TestAppRegistry_GetAllApps(t *testing.T) {
	dir := t.TempDir()

	for _, id := range []string{"a", "b"} {
		appDir := filepath.Join(dir, id)
		os.MkdirAll(appDir, 0755)
		writeTestManifest(t, appDir, id, id+".star")
		os.WriteFile(filepath.Join(appDir, id+".star"), []byte("# app"), 0644)
	}

	reg := NewAppRegistry()
	reg.LoadApps(dir)

	all := reg.GetAllApps()
	if len(all) != 2 {
		t.Errorf("expected 2 apps, got %d", len(all))
	}

	// Verify it's a copy
	all["hacked"] = &AppManifest{ID: "hacked"}
	if _, ok := reg.GetApp("hacked"); ok {
		t.Error("GetAllApps should return a copy")
	}
}

func TestAppRegistry_GetAppsList(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "x")
	os.MkdirAll(appDir, 0755)
	writeTestManifest(t, appDir, "x", "x.star")
	os.WriteFile(filepath.Join(appDir, "x.star"), []byte("# app"), 0644)

	reg := NewAppRegistry()
	reg.LoadApps(dir)

	list := reg.GetAppsList()
	if len(list) != 1 {
		t.Errorf("expected 1 app, got %d", len(list))
	}
}

func TestAppRegistry_LoadApps_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	reg := NewAppRegistry()
	if err := reg.LoadApps(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.GetAppsList()) != 0 {
		t.Error("expected 0 apps for empty dir")
	}
}

func TestAppRegistry_LoadApps_NonexistentDir(t *testing.T) {
	reg := NewAppRegistry()
	err := reg.LoadApps("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestAppRegistry_LoadApps_SkipsInvalid(t *testing.T) {
	dir := t.TempDir()

	// Valid app
	validDir := filepath.Join(dir, "valid")
	os.MkdirAll(validDir, 0755)
	writeTestManifest(t, validDir, "valid", "valid.star")
	os.WriteFile(filepath.Join(validDir, "valid.star"), []byte("# ok"), 0644)

	// Invalid: no star file
	invalidDir := filepath.Join(dir, "broken")
	os.MkdirAll(invalidDir, 0755)
	writeTestManifest(t, invalidDir, "broken", "broken.star")

	// Regular file (not a dir)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("nope"), 0644)

	reg := NewAppRegistry()
	reg.LoadApps(dir)

	if len(reg.GetAppsList()) != 1 {
		t.Errorf("expected 1 valid app, got %d", len(reg.GetAppsList()))
	}
	if _, ok := reg.GetApp("valid"); !ok {
		t.Error("expected 'valid' app to be loaded")
	}
}

func TestAppRegistry_LoadApps_ClearsOnReload(t *testing.T) {
	dir := t.TempDir()

	appDir := filepath.Join(dir, "app")
	os.MkdirAll(appDir, 0755)
	writeTestManifest(t, appDir, "app", "app.star")
	os.WriteFile(filepath.Join(appDir, "app.star"), []byte("# ok"), 0644)

	reg := NewAppRegistry()
	reg.LoadApps(dir)
	if len(reg.GetAppsList()) != 1 {
		t.Fatal("expected 1 app initially")
	}

	// Remove the app and reload
	os.RemoveAll(appDir)
	reg.LoadApps(dir)
	if len(reg.GetAppsList()) != 0 {
		t.Error("expected 0 apps after removing and reloading")
	}
}
