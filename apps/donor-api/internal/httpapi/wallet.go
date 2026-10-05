package httpapi

import (
	"context"
	"net/http"
	"time"
)

func installWalletRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	mux.HandleFunc("GET /donor/me/wallet", func(w http.ResponseWriter, r *http.Request) {
		if services.Donor == nil {
			reply(w, 503, map[string]string{"detail": "Donor account is temporarily unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, err := services.Donor.Sharing(ctx, sessionToken(r))
		if err != nil {
			donorReply(w, reply, nil, err)
			return
		}
		reply(w, 200, map[string]bool{"apple": services.Wallet != nil && services.Wallet.Apple != nil, "google": services.Wallet != nil && services.Wallet.Google != nil})
	})
	issue := func(provider string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !guard.check(w, r, reply) {
				return
			}
			if services.Donor == nil {
				reply(w, 503, map[string]string{"detail": "Donor account is temporarily unavailable"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			details, err := services.Donor.Sharing(ctx, sessionToken(r))
			if err != nil {
				donorReply(w, reply, nil, err)
				return
			}
			if details.IRLLink == nil {
				reply(w, 409, map[string]string{"detail": "Choose a public nickname before adding your code to Wallet"})
				return
			}
			if provider == "google" {
				link, err := services.Wallet.GoogleURL(*details.IRLLink, time.Now())
				if err != nil {
					reply(w, 503, map[string]string{"detail": "Google Wallet is unavailable"})
					return
				}
				reply(w, 200, map[string]string{"url": link})
				return
			}
			data, err := services.Wallet.ApplePass(*details.IRLLink, time.Now())
			if err != nil {
				reply(w, 503, map[string]string{"detail": "Apple Wallet is unavailable"})
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.pkpass")
			w.Header().Set("Content-Disposition", `attachment; filename="dad-referral.pkpass"`)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(data)
		}
	}
	mux.HandleFunc("POST /donor/me/wallet/apple", issue("apple"))
	mux.HandleFunc("POST /donor/me/wallet/google", issue("google"))
}
