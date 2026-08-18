package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSafeJoinResolved_RejectsSymlinkEscape 防止 API 服务 review round-2
// 发现的回归：safeJoin 只做字符串级校验，如果 root 目录内部本身放了一个
// 指向外部的符号链接，字符串校验完全看不出来——必须真正 EvalSymlinks 才
// 能发现。这是白盒测试（package api，不是 api_test），因为 safeJoinResolved
// 是未导出函数。
func TestSafeJoinResolved_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("should not be readable via root"), 0o644); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	// root/escape-link 指向 root 之外的 outside 目录。
	linkPath := filepath.Join(root, "escape-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	_, err := safeJoinResolved(root, "escape-link/secret.txt")
	if err == nil {
		t.Fatal("expected safeJoinResolved to reject a path that escapes root via a symlink")
	}
}

// TestSafeJoinResolved_AcceptsOrdinaryPathWithinRoot 确认修复没有误伤——
// root 内部没有符号链接时，一个普通的合法相对路径应该正常通过。
func TestSafeJoinResolved_AcceptsOrdinaryPathWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mini"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mini", "suite.v1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := safeJoinResolved(root, "mini/suite.v1.json")
	if err != nil {
		t.Fatalf("expected an ordinary in-root path to be accepted, got error: %v", err)
	}
	want := filepath.Join(root, "mini", "suite.v1.json")
	if got != want {
		t.Errorf("expected resolved path %q, got %q", want, got)
	}
}

// TestResolveWithinRoot_AcceptsNotYetExistingPath 确认 resolveWithinRoot
// 能正确处理"文件还不存在，但父目录存在"的写入场景（如即将落盘的报告
// 文件）——不能要求 candidate 本身必须已经存在。
func TestResolveWithinRoot_AcceptsNotYetExistingPath(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "model-x", "run-1-report.html")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := resolveWithinRoot(root, candidate); err != nil {
		t.Errorf("expected a not-yet-existing file under an existing in-root directory to be accepted, got: %v", err)
	}
}
