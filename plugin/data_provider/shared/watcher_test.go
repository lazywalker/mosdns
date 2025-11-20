package shared

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestFileWatcher_BasicReload tests that the file watcher detects simple file writes
func TestFileWatcher_BasicReload(t *testing.T) {
	// Create a temporary directory and file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Track reload calls
	var mu sync.Mutex
	reloadCount := 0
	var lastFilename string

	callback := func(filename string) error {
		mu.Lock()
		defer mu.Unlock()
		reloadCount++
		lastFilename = filename
		return nil
	}

	// Create and start watcher
	logger := zap.NewNop()
	fw := NewFileWatcher(logger, callback, 100*time.Millisecond)
	
	if err := fw.Start([]string{testFile}); err != nil {
		t.Fatalf("failed to start file watcher: %v", err)
	}
	defer fw.Close()

	// Give the watcher time to set up
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Wait for the reload to be triggered
	time.Sleep(300 * time.Millisecond)

	// Verify reload was called
	mu.Lock()
	defer mu.Unlock()
	
	if reloadCount == 0 {
		t.Error("expected at least one reload, got 0")
	}
	
	if lastFilename != testFile {
		t.Errorf("expected filename %s, got %s", testFile, lastFilename)
	}
}

// TestFileWatcher_AtomicReplace tests that the watcher handles atomic file replacements
// (the common pattern used by vim, nano, and other editors)
func TestFileWatcher_AtomicReplace(t *testing.T) {
	// Create a temporary directory and file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Track reload calls
	var mu sync.Mutex
	reloadCount := 0

	callback := func(filename string) error {
		mu.Lock()
		defer mu.Unlock()
		reloadCount++
		return nil
	}

	// Create and start watcher
	logger := zap.NewNop()
	fw := NewFileWatcher(logger, callback, 100*time.Millisecond)
	
	if err := fw.Start([]string{testFile}); err != nil {
		t.Fatalf("failed to start file watcher: %v", err)
	}
	defer fw.Close()

	// Give the watcher time to set up
	time.Sleep(100 * time.Millisecond)

	// Simulate atomic file replacement (like vim does)
	// 1. Create a new temporary file
	tmpFile := filepath.Join(tmpDir, ".test.txt.tmp")
	if err := os.WriteFile(tmpFile, []byte("new content via atomic replace"), 0644); err != nil {
		t.Fatalf("failed to create temporary file: %v", err)
	}

	// 2. Rename/move it over the original file (atomic replacement)
	if err := os.Rename(tmpFile, testFile); err != nil {
		t.Fatalf("failed to rename file: %v", err)
	}

	// Wait for the reload to be triggered
	time.Sleep(300 * time.Millisecond)

	// Get the initial reload count
	mu.Lock()
	firstReloadCount := reloadCount
	mu.Unlock()

	if firstReloadCount == 0 {
		t.Error("expected at least one reload after atomic replacement, got 0")
	}

	// Now test that subsequent writes still work (this is the key test for the bug)
	// This would fail with the old implementation because the file is no longer watched
	if err := os.WriteFile(testFile, []byte("content after atomic replace"), 0644); err != nil {
		t.Fatalf("failed to write to file after atomic replace: %v", err)
	}

	// Wait for the reload to be triggered
	time.Sleep(300 * time.Millisecond)

	// Verify another reload was called
	mu.Lock()
	defer mu.Unlock()
	
	if reloadCount <= firstReloadCount {
		t.Errorf("expected more reloads after subsequent write (first: %d, current: %d)", firstReloadCount, reloadCount)
	}
}

// TestFileWatcher_Debounce tests that the debounce mechanism works
func TestFileWatcher_Debounce(t *testing.T) {
	// Create a temporary directory and file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Track reload calls
	var mu sync.Mutex
	reloadCount := 0

	callback := func(filename string) error {
		mu.Lock()
		defer mu.Unlock()
		reloadCount++
		return nil
	}

	// Create and start watcher with a debounce period
	logger := zap.NewNop()
	fw := NewFileWatcher(logger, callback, 200*time.Millisecond)
	
	if err := fw.Start([]string{testFile}); err != nil {
		t.Fatalf("failed to start file watcher: %v", err)
	}
	defer fw.Close()

	// Give the watcher time to set up and let initial lastReload time pass
	time.Sleep(250 * time.Millisecond)

	// Make the first change - this should trigger a reload
	if err := os.WriteFile(testFile, []byte("change 1"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // Wait for reload to complete

	mu.Lock()
	firstReloadCount := reloadCount
	mu.Unlock()

	if firstReloadCount == 0 {
		t.Fatal("expected first reload to happen")
	}

	// Make multiple rapid changes within debounce period (most should be skipped)
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(testFile, []byte("rapid change"), 0644); err != nil {
			t.Fatalf("failed to modify test file: %v", err)
		}
		time.Sleep(30 * time.Millisecond) // Much less than debounce period
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	secondReloadCount := reloadCount
	mu.Unlock()

	// The rapid changes should have been mostly debounced.
	// We allow up to 2 additional reloads due to multiple WRITE events that
	// os.WriteFile may trigger (e.g., file open, write, close can generate
	// multiple events), but it should be significantly less than the 5 writes
	// we made, demonstrating that debounce is working.
	const maxAllowedExtraReloads = 2
	if secondReloadCount > firstReloadCount+maxAllowedExtraReloads {
		t.Errorf("expected debounce to limit reloads, got %d extra reloads from 5 rapid writes (max allowed: %d)", 
			secondReloadCount-firstReloadCount, maxAllowedExtraReloads)
	}

	// Wait for debounce period to pass
	time.Sleep(300 * time.Millisecond)

	// Make another change - this should trigger another reload
	if err := os.WriteFile(testFile, []byte("change 2"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	finalReloadCount := reloadCount
	mu.Unlock()

	// Should have at least one more reload after debounce period passed
	if finalReloadCount <= secondReloadCount {
		t.Errorf("expected at least one more reload after debounce period, got %d total", finalReloadCount)
	}
}

// TestFileWatcher_MultipleFiles tests watching multiple files
func TestFileWatcher_MultipleFiles(t *testing.T) {
	// Create a temporary directory and files
	tmpDir := t.TempDir()
	testFile1 := filepath.Join(tmpDir, "test1.txt")
	testFile2 := filepath.Join(tmpDir, "test2.txt")
	
	if err := os.WriteFile(testFile1, []byte("file1"), 0644); err != nil {
		t.Fatalf("failed to create test file 1: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("file2"), 0644); err != nil {
		t.Fatalf("failed to create test file 2: %v", err)
	}

	// Track which files triggered reloads
	var mu sync.Mutex
	reloadedFiles := make(map[string]int)

	callback := func(filename string) error {
		mu.Lock()
		defer mu.Unlock()
		reloadedFiles[filename]++
		return nil
	}

	// Create and start watcher
	logger := zap.NewNop()
	fw := NewFileWatcher(logger, callback, 100*time.Millisecond)
	
	if err := fw.Start([]string{testFile1, testFile2}); err != nil {
		t.Fatalf("failed to start file watcher: %v", err)
	}
	defer fw.Close()

	// Give the watcher time to set up
	time.Sleep(100 * time.Millisecond)

	// Modify file 1
	if err := os.WriteFile(testFile1, []byte("modified file1"), 0644); err != nil {
		t.Fatalf("failed to modify test file 1: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Modify file 2
	if err := os.WriteFile(testFile2, []byte("modified file2"), 0644); err != nil {
		t.Fatalf("failed to modify test file 2: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Verify both files triggered reloads
	mu.Lock()
	defer mu.Unlock()
	
	if reloadedFiles[testFile1] == 0 {
		t.Error("expected reload for file1")
	}
	
	if reloadedFiles[testFile2] == 0 {
		t.Error("expected reload for file2")
	}
}
