package main

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func integratedFrontendHandler(root string, backend http.Handler) (http.Handler, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve frontend root: %w", err)
	}
	if info, statErr := os.Stat(filepath.Join(root, "index.html")); statErr != nil || info.IsDir() {
		return nil, fmt.Errorf("frontend index is unavailable under %s", root)
	}
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") ||
			request.URL.Path == "/internal" || strings.HasPrefix(request.URL.Path, "/internal/") {
			backend.ServeHTTP(writer, request)
			return
		}
		if request.URL.Path == "/frontend-healthz" {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok\n"))
			return
		}
		relative := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(request.URL.Path, "/")))
		if relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path := filepath.Join(root, relative)
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
					writer.Header().Set("Content-Type", contentType)
				}
				files.ServeHTTP(writer, request)
				return
			}
		}
		clone := request.Clone(request.Context())
		clone.URL.Path = "/"
		clone.URL.RawPath = ""
		files.ServeHTTP(writer, clone)
	}), nil
}
