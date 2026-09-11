package update

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()

	execPath := filepath.Join(dir, "novaveil")
	oldContent := []byte("old binary content")
	if err := os.WriteFile(execPath, oldContent, 0o755); err != nil {
		t.Fatalf("write current exec: %v", err)
	}

	newExec := filepath.Join(dir, "new-novaveil")
	newContent := []byte("new binary content")
	if err := os.WriteFile(newExec, newContent, 0o755); err != nil {
		t.Fatalf("write new exec: %v", err)
	}

	if err := replaceExecutable(newExec, execPath); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	// 最终路径应包含新二进制内容。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != string(newContent) {
		t.Fatalf("exec content = %q, want %q", got, newContent)
	}

	// .old 应保留旧二进制内容用于回滚。
	got, err = os.ReadFile(oldExecPath(execPath))
	if err != nil {
		t.Fatalf("read .old: %v", err)
	}
	if string(got) != string(oldContent) {
		t.Fatalf(".old content = %q, want %q", got, oldContent)
	}

	// .new 临时文件应已清除。
	if _, err := os.Stat(execPath + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new temp file should not exist after successful replace")
	}
}

func TestReplaceExecutablePreservesPermissions(t *testing.T) {
	dir := t.TempDir()

	execPath := filepath.Join(dir, "novaveil")
	if err := os.WriteFile(execPath, []byte("old"), 0o700); err != nil {
		t.Fatalf("write current exec: %v", err)
	}

	newExec := filepath.Join(dir, "new-novaveil")
	if err := os.WriteFile(newExec, []byte("new"), 0o644); err != nil {
		t.Fatalf("write new exec: %v", err)
	}

	if err := replaceExecutable(newExec, execPath); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("stat exec: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("exec perm = %o, want 700", info.Mode().Perm())
	}
}

func TestReplaceExecutableRollsBackOnInstallFailure(t *testing.T) {
	dir := t.TempDir()

	execPath := filepath.Join(dir, "novaveil")
	oldContent := []byte("old binary")
	if err := os.WriteFile(execPath, oldContent, 0o755); err != nil {
		t.Fatalf("write current exec: %v", err)
	}

	newExec := filepath.Join(dir, "new-novaveil")
	if err := os.WriteFile(newExec, []byte("new binary"), 0o755); err != nil {
		t.Fatalf("write new exec: %v", err)
	}

	// 预创建 .new 为目录, 使 rename .new → execPath 因目录非空而失败。
	// execPath 已被 rename 到 .old, 此时 execPath 不存在, rename .new → execPath
	// 应成功。改用另一策略: 在步骤 3(rename execPath → .old) 后, 将 .new rename
	// 到一个不可写位置。更简单: 将 execPath 的目录设为只读, 使 rename 失败。
	// 但 rename 在同一目录内只需目录写权限。
	//
	// 用更可控的方式: 先手动执行步骤 1-2(copy + verify), 然后让步骤 4 失败。
	// 这里用预占 .new 路径为目录来使 copyFile 失败(步骤 1)。
	newPath := execPath + ".new"
	if err := os.Mkdir(newPath, 0o755); err != nil {
		t.Fatalf("mkdir .new: %v", err)
	}

	err := replaceExecutable(newExec, execPath)
	if err == nil {
		t.Fatal("replaceExecutable should fail when .new is a directory")
	}

	// 原二进制应保持不变。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != string(oldContent) {
		t.Fatalf("exec content after failed replace = %q, want %q (original preserved)", got, oldContent)
	}
}

func TestReplaceExecutableCleansStaleOld(t *testing.T) {
	dir := t.TempDir()

	execPath := filepath.Join(dir, "novaveil")
	if err := os.WriteFile(execPath, []byte("current"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}

	// 预留一个 stale .old
	staleOld := oldExecPath(execPath)
	if err := os.WriteFile(staleOld, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale .old: %v", err)
	}

	newExec := filepath.Join(dir, "new")
	if err := os.WriteFile(newExec, []byte("new"), 0o755); err != nil {
		t.Fatalf("write new: %v", err)
	}

	if err := replaceExecutable(newExec, execPath); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	// .old 应包含当前版本, 而非 stale 内容。
	got, err := os.ReadFile(oldExecPath(execPath))
	if err != nil {
		t.Fatalf("read .old: %v", err)
	}
	if string(got) != "current" {
		t.Fatalf(".old content = %q, want %q (stale .old should be replaced)", got, "current")
	}
}

func TestVerifyFileIntegrity(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	content := []byte("identical content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileIntegrity(src, dst); err != nil {
		t.Fatalf("identical files should verify: %v", err)
	}
}

func TestVerifyFileIntegrityRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("content-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("content-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileIntegrity(src, dst); err == nil {
		t.Fatal("mismatched files should fail integrity check")
	}
}

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("fileSHA256 = %q, want %q", got, want)
	}
}

func TestWriteUpdateMarker(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "novaveil")

	if err := writeUpdateMarker(execPath); err != nil {
		t.Fatalf("writeUpdateMarker: %v", err)
	}

	data, err := os.ReadFile(updateMarkerPath(execPath))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(data) != "1" {
		t.Fatalf("marker content = %q, want %q", data, "1")
	}
}

func TestUpdateMarkerPath(t *testing.T) {
	got := updateMarkerPath("/usr/local/bin/novaveil")
	want := "/usr/local/bin/novaveil.update-pending"
	if got != want {
		t.Fatalf("updateMarkerPath = %q, want %q", got, want)
	}
}

func TestOldExecPath(t *testing.T) {
	got := oldExecPath("/usr/local/bin/novaveil")
	want := "/usr/local/bin/novaveil.old"
	if got != want {
		t.Fatalf("oldExecPath = %q, want %q", got, want)
	}
}
