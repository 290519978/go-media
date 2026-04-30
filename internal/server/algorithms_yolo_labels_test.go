package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYoloLabelsFromFileSuccessAndSorted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yolo-label.json")
	content := `[
		{"label":"car","name":"车辆"},
		{"label":"person","name":"人员"},
		{"label":"apple","name":"苹果"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	items, err := loadYoloLabelsFromFile(path)
	if err != nil {
		t.Fatalf("load labels failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 labels, got %d", len(items))
	}
	if items[0].Label != "apple" || items[1].Label != "car" || items[2].Label != "person" {
		t.Fatalf("labels are not sorted by label: %+v", items)
	}
}

func TestLoadYoloLabelsFromFileMissingFile(t *testing.T) {
	_, err := loadYoloLabelsFromFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadYoloLabelsFromFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yolo-label.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	_, err := loadYoloLabelsFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestLoadYoloLabelsFromFileDuplicateLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yolo-label.json")
	content := `[
		{"label":"person","name":"人员"},
		{"label":"PERSON","name":"人员-重复"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	_, err := loadYoloLabelsFromFile(path)
	if err == nil {
		t.Fatal("expected duplicate label error")
	}
}

func TestLoadYoloLabelsFromPathCachesSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yolo-label.json")
	content := `[
		{"label":"person","name":"人员"},
		{"label":"car","name":"车辆"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	s := &Server{}
	if err := s.loadYoloLabelsFromPath(path); err != nil {
		t.Fatalf("load labels on startup failed: %v", err)
	}

	items := s.getYoloLabelsSnapshot()
	if len(items) != 2 {
		t.Fatalf("expected 2 cached labels, got %d", len(items))
	}
	if items[0].Label != "car" || items[1].Label != "person" {
		t.Fatalf("expected sorted labels in cache, got %+v", items)
	}
}

func TestGetYoloLabelsSnapshotReturnsCopy(t *testing.T) {
	s := &Server{
		yoloLabels: []yoloLabelItem{
			{Label: "person", Name: "人员"},
		},
	}

	snapshot := s.getYoloLabelsSnapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 label, got %d", len(snapshot))
	}
	snapshot[0].Name = "被篡改"

	again := s.getYoloLabelsSnapshot()
	if again[0].Name != "人员" {
		t.Fatalf("cache should be immutable via snapshot, got %q", again[0].Name)
	}
}
