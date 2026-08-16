package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"companion-server/internal/controlplane"
	"companion-server/internal/onboarding"
	"companion-server/internal/ownerauth"
)

const ownerPathPrefix = "/v1/owner/"

func ownerAuthFromEnvironment(next http.Handler, claimRepository controlplane.DeviceClaimRepository) http.Handler {
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
	owner := limitOwnerLogins(service.Handler())
	claimCodeRedeem := limitOwnerClaimCodeRedeems(http.HandlerFunc(service.HandleHumanClaimCodeRedeem))
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/owner/claim-authorizations":
			// Product composition hard-cuts the older bootstrap-only handler inside
			// ownerauth.Service.Handler: authorization must bind bootstrap + device.
			service.HandleBoundClaimAuthorization(w, r)
			return
		case "/v1/owner/device-claim-code":
			service.HandleHumanClaimCode(w, r)
			return
		case "/v1/owner/device-claim-codes/redeem":
			claimCodeRedeem.ServeHTTP(w, r)
			return
		case "/v1/owner/device-claims":
			if claims == nil {
				http.Error(w, "device claim bootstrap unavailable", http.StatusServiceUnavailable)
				return
			}
			claims.ServeHTTP(w, r)
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
