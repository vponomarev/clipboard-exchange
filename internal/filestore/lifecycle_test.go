package filestore

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceSnapshotsAfterActivePublication(t *testing.T) {
	files, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := files.BeginOperation()
	// These references become visible only when publication has completed.
	uploads, objects := map[string]bool{}, map[string]bool{}
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(entered); files.Maintenance(func() { done <- files.Reconcile(uploads, objects) }) }()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("maintenance ran during publication: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, _, _, err := files.WriteChunk(testUploadID, 0, strings.NewReader("abc"), 3); err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := files.Assemble(testUploadID, testFileID, 1, 3); err != nil {
		release()
		t.Fatal(err)
	}
	uploads[testUploadID], objects[testFileID] = true, true
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not finish")
	}
	object, err := files.OpenObject(testFileID)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	data, err := io.ReadAll(object)
	if err != nil || string(data) != "abc" {
		t.Fatalf("active object lost: %q %v", data, err)
	}
}

func TestBusyMaintenanceDoesNotBlockNewRequests(t *testing.T) {
	files, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := files.BeginOperation()
	if files.TryMaintenance(func() { t.Error("maintenance ran during transfer") }) {
		t.Fatal("busy maintenance succeeded")
	}
	second := files.BeginOperation()
	second()
	first()
	called := false
	if !files.TryMaintenance(func() { called = true }) || !called {
		t.Fatal("idle maintenance was skipped")
	}
}
