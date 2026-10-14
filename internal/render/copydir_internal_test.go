package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise the unexported copyDir directly, so this file lives
// in package render rather than render_test.

// TestCopyDirFollowsASymlinkedFile locks in behaviour that must NOT change:
// a symlink to a regular file is a normal, desirable way to share an asset,
// and must be copied as its target's contents, not skipped or rejected.
func TestCopyDirFollowsASymlinkedFile(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	real := filepath.Join(src, "real.txt")
	if err := os.WriteFile(real, []byte("asset contents"), 0o600); err != nil {
		t.Fatalf("write real file: %v", err)
	}
	link := filepath.Join(src, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("read copied symlink target: %v", err)
	}
	if string(got) != "asset contents" {
		t.Errorf("copied content = %q, want %q", got, "asset contents")
	}
}

// TestCopyDirRejectsASymlinkedDirectoryWithAClearError locks in the fix: a
// symlink to a directory now fails with a message naming the path and the
// reason, instead of copyFile's low-level "is a directory" error that named
// neither.
func TestCopyDirRejectsASymlinkedDirectoryWithAClearError(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	realSub := filepath.Join(src, "real-sub")
	if err := os.MkdirAll(realSub, 0o750); err != nil {
		t.Fatalf("mkdir real-sub: %v", err)
	}
	linkedDir := filepath.Join(src, "linked-dir")
	if err := os.Symlink(realSub, linkedDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := copyDir(src, dst)
	if err == nil {
		t.Fatal("want an error for a symlinked directory")
	}
	if !strings.Contains(err.Error(), linkedDir) {
		t.Errorf("error %q does not name the offending path %q", err, linkedDir)
	}
}

// TestCopyDirDoesNotRecurseUnboundedlyOnASymlinkLoop empirically refutes the
// claim that a symlink loop causes unbounded recursion: fs.DirEntry.IsDir()
// uses Lstat semantics, so the loop-back entry reports IsDir()==false and is
// never recursed into — it hits the symlinked-directory check above and
// returns a clear error immediately. This test would hang (and eventually
// fail on timeout) if that stopped being true.
func TestCopyDirDoesNotRecurseUnboundedlyOnASymlinkLoop(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	loopDir := filepath.Join(src, "loop")
	if err := os.MkdirAll(loopDir, 0o750); err != nil {
		t.Fatalf("mkdir loop: %v", err)
	}
	if err := os.Symlink(loopDir, filepath.Join(loopDir, "self")); err != nil {
		t.Fatalf("symlink loop: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- copyDir(src, dst) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error for a symlink loop, not silent success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyDir did not return within 5s on a symlink loop — looks like unbounded recursion")
	}
}
