package httpapi

import (
	"net/http"
	"time"
)

const walletUnavailable = "Choose a public nickname before adding your code to Wallet"

func (a *api) walletProviders(w http.ResponseWriter, r *http.Request, session string) {
	if !a.donorAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	_, err := a.Donor.Sharing(ctx, session)
	respond(w, r, map[string]bool{
		"apple":  a.Wallet != nil && a.Wallet.Apple != nil,
		"google": a.Wallet != nil && a.Wallet.Google != nil,
	}, err, donorStatus)
}

func (a *api) googlePass(w http.ResponseWriter, r *http.Request, session string) {
	link, ok := a.irlLink(w, r, session, walletUnavailable)
	if !ok {
		return
	}
	if a.Wallet == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Google Wallet is unavailable", nil)
		return
	}
	url, err := a.Wallet.GoogleURL(link, time.Now())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "Google Wallet is unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (a *api) applePass(w http.ResponseWriter, r *http.Request, session string) {
	link, ok := a.irlLink(w, r, session, walletUnavailable)
	if !ok {
		return
	}
	if a.Wallet == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Apple Wallet is unavailable", nil)
		return
	}
	data, err := a.Wallet.ApplePass(link, time.Now())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "Apple Wallet is unavailable", err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.pkpass")
	w.Header().Set("Content-Disposition", `attachment; filename="dad-referral.pkpass"`)
	_, _ = w.Write(data)
}
