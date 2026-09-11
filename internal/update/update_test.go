package update

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyReleaseChecksum(t *testing.T) {
	archive := []byte("release-archive")
	sum := sha256.Sum256(archive)
	manifest := []byte(hex.EncodeToString(sum[:]) + "  novaveil-linux-amd64.zip\n")
	if err := verifyReleaseChecksum("novaveil-linux-amd64.zip", archive, manifest); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if err := verifyReleaseChecksum("novaveil-linux-amd64.zip", []byte("tampered"), manifest); err == nil {
		t.Fatal("tampered archive must be rejected")
	}
	if err := verifyReleaseChecksum("missing.zip", archive, manifest); err == nil {
		t.Fatal("missing checksum entry must be rejected")
	}
}

func TestReadAllLimited(t *testing.T) {
	got, err := readAllLimited(strings.NewReader("abcd"), 4)
	if err != nil {
		t.Fatalf("readAllLimited exact limit: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("body = %q, want abcd", got)
	}

	if _, err := readAllLimited(strings.NewReader("abcde"), 4); err == nil || !strings.Contains(err.Error(), "响应正文超过大小限制") || !errors.Is(err, errSizeLimitExceeded) {
		t.Fatalf("expected identifiable size limit error, got %v", err)
	}
}

func TestDoRequestWithLimitRejectsKnownAndUnknownLengths(t *testing.T) {
	tests := []struct {
		name  string
		serve func(http.ResponseWriter)
	}{
		{
			name: "known content length",
			serve: func(w http.ResponseWriter) {
				w.Header().Set("Content-Length", "5")
				_, _ = io.WriteString(w, "abcde")
			},
		},
		{
			name: "chunked unknown length",
			serve: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				_, _ = io.WriteString(w, "abcde")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tt.serve(w)
			}))
			defer server.Close()

			if _, err := doRequestWithLimit(server.URL, false, 4); err == nil || !strings.Contains(err.Error(), "响应正文超过大小限制") || !errors.Is(err, errSizeLimitExceeded) {
				t.Fatalf("expected identifiable size limit error, got %v", err)
			}
		})
	}
}

func TestCopyLimitedReadsLimitPlusOne(t *testing.T) {
	var dst bytes.Buffer
	written, err := copyLimited(&dst, strings.NewReader("abcde"), 4)
	if !errors.Is(err, errSizeLimitExceeded) {
		t.Fatalf("expected size limit error, got %v", err)
	}
	if written != 5 || dst.String() != "abcde" {
		t.Fatalf("copy result = (%d, %q), want limit+1 bytes", written, dst.String())
	}
}

func TestUnzipExtractsValidArchive(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{
		"novaveil":         "binary",
		"nested/readme":    "docs",
		"nested/config.js": "cfg",
	})

	err := unzip(data, dest)
	if err != nil {
		t.Fatalf("unzipWithLimits valid archive: %v", err)
	}

	assertFileContent(t, filepath.Join(dest, "novaveil"), "binary")
	assertFileContent(t, filepath.Join(dest, "nested", "readme"), "docs")
	assertFileContent(t, filepath.Join(dest, "nested", "config.js"), "cfg")
}

func TestUnzipWithLimitsRejectsTooManyFiles(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{"a": "1", "b": "2"})

	err := unzipWithLimits(data, dest, unzipLimits{MaxFiles: 1, MaxFileBytes: 16, MaxTotalBytes: 32})
	if err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Fatalf("expected too many files error, got %v", err)
	}
}

func TestUnzipWithLimitsRejectsSingleFileByDeclaredSize(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{"big": "12345"})

	err := unzipWithLimits(data, dest, unzipLimits{MaxFiles: 1, MaxFileBytes: 4, MaxTotalBytes: 32})
	if err == nil || !strings.Contains(err.Error(), "zip file too large") {
		t.Fatalf("expected zip file too large error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "big")); !os.IsNotExist(statErr) {
		t.Fatalf("oversized file should not be extracted, stat err=%v", statErr)
	}
}

func TestUnzipWithLimitsRejectsTotalExpandedSize(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{"a": "1234", "b": "5678"})

	err := unzipWithLimits(data, dest, unzipLimits{MaxFiles: 2, MaxFileBytes: 8, MaxTotalBytes: 7})
	if err == nil || !strings.Contains(err.Error(), "expanded size too large") {
		t.Fatalf("expected expanded size too large error, got %v", err)
	}
}

func TestUnzipWithLimitsAllowsEmptyFileAtExactTotalLimit(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{"a": "1234", "empty": ""})

	err := unzipWithLimits(data, dest, unzipLimits{MaxFiles: 2, MaxFileBytes: 4, MaxTotalBytes: 4})
	if err != nil {
		t.Fatalf("unzip exact total with empty file: %v", err)
	}
	assertFileContent(t, filepath.Join(dest, "a"), "1234")
	assertFileContent(t, filepath.Join(dest, "empty"), "")
}

func TestUnzipWithLimitsKeepsZipSlipProtection(t *testing.T) {
	dest := t.TempDir()
	data := buildZip(t, map[string]string{"../escape": "bad"})

	err := unzipWithLimits(data, dest, unzipLimits{MaxFiles: 1, MaxFileBytes: 16, MaxTotalBytes: 32})
	if err == nil || !strings.Contains(err.Error(), "invalid file path") {
		t.Fatalf("expected invalid file path error, got %v", err)
	}
}

func TestUnzipWithLimitsSkipsSymlinkEntries(t *testing.T) {
	dest := t.TempDir()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatalf("create symlink entry: %v", err)
	}
	if _, err := io.WriteString(w, "target"); err != nil {
		t.Fatalf("write symlink entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	err = unzipWithLimits(buf.Bytes(), dest, unzipLimits{MaxFiles: 1, MaxFileBytes: 16, MaxTotalBytes: 32})
	if err != nil {
		t.Fatalf("unzipWithLimits symlink archive: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dest, "link")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink entry should be skipped, stat err=%v", statErr)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
