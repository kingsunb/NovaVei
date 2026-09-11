package relay

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/looplj/axonhub/llm/httpclient"
)

const (
	maxRelayCompressedBodyBytes   int64 = 16 * 1024 * 1024
	maxRelayDecompressedBodyBytes int64 = 64 * 1024 * 1024
)

var ErrRelayBodyTooLarge = errors.New("request body too large")

func readLimitedHTTPRequest(rawReq *http.Request) (*httpclient.Request, error) {
	req := &httpclient.Request{
		Method:     rawReq.Method,
		URL:        rawReq.URL.String(),
		Path:       rawReq.URL.Path,
		Query:      rawReq.URL.Query(),
		Headers:    rawReq.Header.Clone(),
		Auth:       &httpclient.AuthConfig{},
		ClientIP:   clientIP(rawReq),
		RawRequest: rawReq,
	}

	body, err := readLimit(rawReq.Body, maxRelayCompressedBodyBytes)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		body, err = decodeLimitedBody(body, req.Headers, maxRelayDecompressedBodyBytes)
		if err != nil {
			return nil, err
		}
	}
	req.Body = body
	return req, nil
}

func decodeLimitedBody(body []byte, headers http.Header, limit int64) ([]byte, error) {
	encoding := strings.ToLower(strings.TrimSpace(headers.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		if int64(len(body)) > limit {
			return nil, ErrRelayBodyTooLarge
		}
		return body, nil
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer reader.Close()
		return readDecoded(reader, headers, limit, "gzip")
	case "deflate":
		decoded, err := decodeLimitedDeflate(body, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress deflate body: %w", err)
		}
		headers.Del("Content-Encoding")
		headers.Del("Content-Length")
		return decoded, nil
	case "zstd":
		decoder, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderMaxMemory(uint64(limit)))
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		defer decoder.Close()
		return readDecoded(decoder, headers, limit, "zstd")
	default:
		return nil, fmt.Errorf("unsupported content encoding: %s", headers.Get("Content-Encoding"))
	}
}

func decodeLimitedDeflate(body []byte, limit int64) ([]byte, error) {
	if reader, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
		defer reader.Close()
		return readLimit(reader, limit)
	}
	reader := flate.NewReader(bytes.NewReader(body))
	defer reader.Close()
	return readLimit(reader, limit)
}

func readDecoded(reader io.Reader, headers http.Header, limit int64, encoding string) ([]byte, error) {
	decoded, err := readLimit(reader, limit)
	if err != nil {
		if errors.Is(err, ErrRelayBodyTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to decompress %s body: %w", encoding, err)
	}
	headers.Del("Content-Encoding")
	headers.Del("Content-Length")
	return decoded, nil
}

func readLimit(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrRelayBodyTooLarge
	}
	return data, nil
}

func clientIP(req *http.Request) string {
	if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return ip
	}
	return req.RemoteAddr
}
