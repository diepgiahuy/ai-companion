package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/onboarding"
	"companion-server/internal/ownerauth"
	"companion-server/internal/ownerweb"
)

const ownerPathPrefix = "/v1/owner/"

func ownerAuthFromEnvironment(next http.Handler, store domain.ReadRepositories, claimRepository controlplane.DeviceClaimRepository) http.Handler {
	cfg := ownerauth.Config{
		AuthorizationURL: strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_AUTH_URL")),
		TokenURL:         strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_TOKEN_URL")),
		UserInfoURL:      strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_USERINFO_URL")),
		ClientID:         strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_CLIENT_ID")),
		ClientSecret:     os.Getenv("COMPANION_OWNER_OIDC_CLIENT_SECRET"),
		RedirectURL:      strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_REDIRECT_URL")),
		Scopes:           ownerScopes(os.Getenv("COMPANION_OWNER_OIDC_SCOPES")),
	}
	configured := cfg.AuthorizationURL != "" || cfg.TokenURL != "" || cfg.UserInfoURL != "" ||
		cfg.ClientID != "" || cfg.ClientSecret != "" || cfg.RedirectURL != "" ||
		strings.TrimSpace(os.Getenv("COMPANION_OWNER_OIDC_SCOPES")) != ""
	if !configured {
		if store != nil {
			webHandler := ownerweb.NewHandler(ownerweb.Dependencies{Store: store, Auth: nil})
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/owner/dashboard" || strings.HasPrefix(r.URL.Path, "/v1/owner/data/") {
					webHandler.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
		return next
	}
	service, err := ownerauth.New(cfg)
	if err != nil {
		slog.Error("owner OIDC configuration rejected", "error", err)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, ownerPathPrefix) {
				http.Error(w, "owner authentication unavailable", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	owner := service.Handler()
	var claims http.Handler
	if claimRepository != nil {
		key, keyErr := onboarding.DecodeEncryptionKey(os.Getenv("COMPANION_BOOTSTRAP_ENCRYPTION_KEY"))
		if keyErr != nil {
			slog.Warn("device claim bootstrap disabled", "error", keyErr)
		} else if claimService, claimErr := onboarding.NewClaimService(claimRepository, service, key); claimErr != nil {
			slog.Error("initialize device claim service", "error", claimErr)
		} else {
			claims = claimService.Handler()
		}
	}
	var webHandler http.Handler
	if store != nil {
		webHandler = ownerweb.NewHandler(ownerweb.Dependencies{Store: store, Auth: service})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/owner/claim-authorizations":
			service.HandleBoundClaimAuthorization(w, r)
			return
		case r.URL.Path == "/v1/owner/device-claim-code":
			service.HandleHumanClaimCode(w, r)
			return
		case r.URL.Path == "/v1/owner/device-claim-codes/redeem":
			service.HandleHumanClaimCodeRedeem(w, r)
			return
		case r.URL.Path == "/v1/owner/device-claims":
			if claims == nil {
				http.Error(w, "device claim bootstrap unavailable", http.StatusServiceUnavailable)
				return
			}
			claims.ServeHTTP(w, r)
			return
		case webHandler != nil && (r.URL.Path == "/v1/owner/dashboard" || strings.HasPrefix(r.URL.Path, "/v1/owner/data/")):
			webHandler.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, ownerPathPrefix) {
			owner.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ownerScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"openid", "profile"}
	}
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}
