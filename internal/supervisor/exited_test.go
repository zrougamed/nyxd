package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExitedRecords(t *testing.T) {
	s := &Supervisor{baseDir: t.TempDir(), log: quiet, containers: map[string]*containerEntry{}}
	if ids := s.ExitedIDs(); len(ids) != 0 {
		t.Errorf("empty: %v", ids)
	}

	blob := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(blob, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.recordExited(ContainerSpec{ID: "web", Image: "nginx:1", BlobPaths: []string{blob}}, 137)
	s.recordExited(ContainerSpec{ID: "tmp", AutoRemove: true}, 0) // --rm: no record
	s.recordExited(ContainerSpec{}, 0)                            // no id: no record

	if ids := s.ExitedIDs(); strings.Join(ids, ",") != "web" {
		t.Fatalf("ids: %v", ids)
	}
	info := s.ExitedInfo()
	if len(info) != 1 || info[0].Status != "exited" || *info[0].ExitCode != 137 || info[0].Image != "nginx:1" {
		t.Errorf("info: %+v", info)
	}
	if refs := s.ImageRefsInUse(); len(refs) != 1 {
		t.Errorf("image refs: %v", refs)
	}
	if st, err := os.Stat(s.exitedPath("web")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("record mode: %v %v", st, err)
	}

	// A running container with the same id hides its old record.
	s.containers["web"] = &containerEntry{}
	if len(s.ExitedInfo()) != 0 {
		t.Error("running container listed as exited")
	}
	if err := s.StartExisting(context.Background(), "web"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("start running: %v", err)
	}
	delete(s.containers, "web")

	// Unreadable or mismatched records are ignored.
	if err := os.WriteFile(s.exitedPath("bad"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.exitedPath("other"), []byte(`{"spec":{"ID":"web"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.exitedDir(), "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if info := s.ExitedInfo(); len(info) != 1 {
		t.Errorf("bad records listed: %+v", info)
	}
	if _, ok := s.readExited("bad"); ok {
		t.Error("corrupt record read")
	}
	if err := s.StartExisting(context.Background(), "bad"); !errors.Is(err, ErrNoContainer) {
		t.Errorf("start corrupt: %v", err)
	}
	if err := s.Restart(context.Background(), "nosuch", 0); !errors.Is(err, ErrNoContainer) {
		t.Errorf("restart missing: %v", err)
	}

	// Remove of an exited container forgets it.
	if err := s.Remove(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.readExited("web"); ok {
		t.Error("record kept after remove")
	}
	if err := s.Remove(context.Background(), "web"); err == nil {
		t.Error("second remove succeeded")
	}
}

func TestRecordExitedWriteFailure(t *testing.T) {
	base := t.TempDir()
	// supervisor/ is a file: the record cannot be written, which is only logged.
	if err := os.WriteFile(filepath.Join(base, "supervisor"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{baseDir: base, log: quiet}
	s.recordExited(ContainerSpec{ID: "x"}, 0)
	if len(s.ExitedIDs()) != 0 {
		t.Error("record written")
	}
}

func TestRefreshImage(t *testing.T) {
	s := &Supervisor{log: quiet}
	blob := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(blob, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := ContainerSpec{ID: "c", Image: "app:1", BlobPaths: []string{blob}}
	if got, err := s.refreshImage(spec); err != nil || got.BlobPaths[0] != blob {
		t.Errorf("layers present: %v %v", got, err)
	}
	// Pruned layers and no store: keep the spec, Start reports the error.
	spec.BlobPaths = []string{filepath.Join(t.TempDir(), "gone")}
	if got, err := s.refreshImage(spec); err != nil || got.BlobPaths[0] != spec.BlobPaths[0] {
		t.Errorf("no store: %v %v", got, err)
	}
}

func TestFinishedChan(t *testing.T) {
	e := &containerEntry{}
	ch := e.finishedChan()
	select {
	case <-ch:
		t.Fatal("closed before finish")
	default:
	}
	e.markFinished()
	e.markFinished() // twice is fine
	if !waitClosed(ch, time.Second) {
		t.Fatal("not closed after finish")
	}
	// Asked for after the monitor returned: already closed.
	late := &containerEntry{}
	late.markFinished()
	if !waitClosed(late.finishedChan(), time.Second) {
		t.Fatal("late chan not closed")
	}
}
