package update

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/kingsunb/NovaVei/internal/client"
	"github.com/kingsunb/NovaVei/internal/conf"
)

const (
	updateUrl    = "https://github.com/kingsunb/NovaVei/releases/latest/download"
	updateApiUrl = "https://api.github.com/repos/kingsunb/NovaVei/releases/latest"
)

const (
	maxUpdateDownloadBytes = 256 * 1024 * 1024
	maxZipFiles            = 1024
	maxZipFileBytes        = 256 * 1024 * 1024
	maxZipTotalBytes       = 512 * 1024 * 1024
)

var errSizeLimitExceeded = errors.New("size limit exceeded")

type unzipLimits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

var defaultUnzipLimits = unzipLimits{
	MaxFiles:      maxZipFiles,
	MaxFileBytes:  maxZipFileBytes,
	MaxTotalBytes: maxZipTotalBytes,
}

type LatestInfo struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Message     string `json:"message"`
}

var github_pat = os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT")

// doRequestWithFallback performs an HTTP GET request, first without proxy, then with proxy if failed.
func doRequestWithFallback(url string) ([]byte, error) {
	data, err := doRequest(url, false)
	if err == nil {
		return data, nil
	}
	if errors.Is(err, errSizeLimitExceeded) {
		return nil, err
	}
	log.Warnf("direct request failed, trying with proxy: %v", err)
	return doRequest(url, true)
}

func doRequest(url string, useProxy bool) ([]byte, error) {
	return doRequestWithLimit(url, useProxy, maxUpdateDownloadBytes)
}

func doRequestWithLimit(url string, useProxy bool, maxBytes int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hc, err := client.GetHTTPClientSystemProxy(useProxy)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Debugf("new request failed: %v", err)
		return nil, err
	}

	if github_pat != "" {
		req.Header.Set("Authorization", "Bearer "+github_pat)
	}

	resp, err := hc.Do(req)
	if err != nil {
		log.Debugf("request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()
	// 非 2xx 一律按失败处理: GitHub 的 404/403/429 返回 HTML/JSON 错误页,
	// 不检查状态码会让错误页流入后续 JSON/unzip/校验流程, 以误导性的
	// "压缩包损坏/校验失败"收场, 用户无从区分网络权限问题与版本损坏。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := readAllLimited(resp.Body, 256)
		return nil, fmt.Errorf("下载更新文件失败: HTTP %s %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("响应正文超过大小限制: 上限 %d 字节: %w", maxBytes, errSizeLimitExceeded)
	}

	data, err := readAllLimited(resp.Body, maxBytes)
	if err != nil {
		log.Debugf("read body failed: %v", err)
		return nil, err
	}
	return data, nil
}

func GetLatestInfo() (*LatestInfo, error) {
	body, err := doRequestWithFallback(updateApiUrl)
	if err != nil {
		return nil, err
	}

	var latestInfo LatestInfo
	if err := json.Unmarshal(body, &latestInfo); err != nil {
		log.Debugf("unmarshal body failed: %v", err)
		return nil, err
	}
	if latestInfo.Message != "" {
		return nil, fmt.Errorf("获取最新版本信息失败: %s", latestInfo.Message)
	}
	return &latestInfo, nil
}

func unzip(data []byte, dest string) error {
	return unzipWithLimits(data, dest, defaultUnzipLimits)
}

func unzipWithLimits(data []byte, dest string, limits unzipLimits) error {
	if limits.MaxFiles <= 0 || limits.MaxFileBytes <= 0 || limits.MaxTotalBytes <= 0 {
		return fmt.Errorf("invalid unzip limits")
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Debugf("new zip reader failed: %v", err)
		return err
	}
	if len(r.File) > limits.MaxFiles {
		return fmt.Errorf("zip contains too many files: %d > %d", len(r.File), limits.MaxFiles)
	}

	var total int64
	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			os.MkdirAll(fpath, 0o755)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if f.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return fmt.Errorf("zip file too large: %s", f.Name)
		}
		if f.UncompressedSize64 > uint64(limits.MaxTotalBytes-total) {
			return fmt.Errorf("zip expanded size too large")
		}

		written, err := extractFile(f, fpath, limits.MaxFileBytes, limits.MaxTotalBytes-total)
		if err != nil {
			return err
		}
		total += written
	}
	return nil
}

func extractFile(f *zip.File, fpath string, maxFileBytes, remainingTotalBytes int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return 0, err
	}

	// 固定文件权限: 普通文件 0o644, 可执行文件 0o755。
	// 不使用 zip 文件自带的 mode bits, 防止恶意 zip 设置 setuid/setgid 或全局可写位。
	filePerm := os.FileMode(0o644)
	if f.Mode()&0o100 != 0 {
		filePerm = 0o755
	}
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return 0, err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return 0, err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return 0, err
	}
	defer rc.Close()

	limit := min(maxFileBytes, remainingTotalBytes)
	written, err := copyLimited(outFile, rc, limit)
	if err != nil {
		_ = outFile.Close()
		_ = os.Remove(fpath)
		if errors.Is(err, errSizeLimitExceeded) {
			if maxFileBytes <= remainingTotalBytes {
				return written, fmt.Errorf("zip file too large: %s", f.Name)
			}
			return written, fmt.Errorf("zip expanded size too large")
		}
		log.Debugf("copy failed: %v", err)
		return written, err
	}
	return written, nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}

func copyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("invalid copy limit")
	}
	written, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return written, err
	}
	if written > limit {
		return written, errSizeLimitExceeded
	}
	return written, nil
}

func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid read limit")
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("响应正文超过大小限制: 上限 %d 字节: %w", limit, errSizeLimitExceeded)
	}
	return data, nil
}
