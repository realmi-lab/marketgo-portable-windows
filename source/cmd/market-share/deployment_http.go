package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"market-share-go/internal/app"
)

type deploymentControlService interface {
	DeploymentStatus() app.DeploymentStatus
	PrepareDeployment(context.Context) error
	PromoteDeployment(context.Context) error
	DemoteDeployment(context.Context, uint64) error
	AbortDeployment(context.Context) error
}

func withDeploymentControl(next http.Handler, service *app.Service, token string, logger *slog.Logger) http.Handler {
	if strings.TrimSpace(token) == "" {
		return next
	}
	return deploymentControlHandler(next, service, token, logger)
}

func deploymentControlHandler(next http.Handler, service deploymentControlService, token string, logger *slog.Logger) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/internal/deployment/") {
			next.ServeHTTP(response, request)
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
		got := sha256.Sum256([]byte(provided))
		if provided == "" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			http.Error(response, "deployment authorization failed", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/internal/deployment/status" {
			if request.Method != http.MethodGet {
				http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			_ = json.NewEncoder(response).Encode(service.DeploymentStatus())
			return
		}
		if request.Method != http.MethodPost {
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var err error
		switch request.URL.Path {
		case "/internal/deployment/prepare":
			err = service.PrepareDeployment(request.Context())
		case "/internal/deployment/promote":
			err = service.PromoteDeployment(request.Context())
		case "/internal/deployment/demote":
			var generation uint64
			if raw := strings.TrimSpace(request.Header.Get("X-MarketGo-Checkpoint-Generation")); raw != "" {
				generation, err = strconv.ParseUint(raw, 10, 64)
				if err != nil || generation == 0 {
					http.Error(response, "invalid checkpoint generation", http.StatusBadRequest)
					return
				}
			}
			err = service.DemoteDeployment(request.Context(), generation)
		case "/internal/deployment/abort":
			err = service.AbortDeployment(request.Context())
		default:
			http.NotFound(response, request)
			return
		}
		if err != nil {
			logger.Warn("deployment control rejected", "path", request.URL.Path, "error", err)
			http.Error(response, err.Error(), http.StatusConflict)
			return
		}
		_ = json.NewEncoder(response).Encode(service.DeploymentStatus())
	})
}
