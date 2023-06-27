// Package cloudflarebp provides a round tripper to not get detected by CloudFlare directly on the first HTTP request
// The round tripper will add required/validated request headers and updates the client TLS configuration
// It'll NOT solve challenges provided by CloudFlare, just prevent from being detected on the first request
package discordgo

import (
	"crypto/tls"
	"net/http"
)

// cloudFlareRoundTripper is a custom round tripper add the validated request headers.
type cloudFlareRoundTripper struct {
	inner   http.RoundTripper
}

// AddCloudFlareByPass returns a round tripper adding the required headers for the CloudFlare checks
// and updates the TLS configuration of the passed inner transport.
func RoundTripper(inner http.RoundTripper) http.RoundTripper {
	if trans, ok := inner.(*http.Transport); ok {
		trans.TLSClientConfig = getCloudFlareTLSConfiguration()
	}

	roundTripper := &cloudFlareRoundTripper{inner: inner}

	return roundTripper
}

// RoundTrip adds the required request headers to pass CloudFlare checks.
func (ug *cloudFlareRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	// in case we don't have an inner transport layer from the round tripper
	if ug.inner == nil {
		return (&http.Transport{
			TLSClientConfig: getCloudFlareTLSConfiguration(),
		}).RoundTrip(r)
	}

	return ug.inner.RoundTrip(r)
}

// getCloudFlareTLSConfiguration returns an accepted client TLS configuration to not get detected by CloudFlare directly
// in case the configuration needs to be updated later on: https://wiki.mozilla.org/Security/Server_Side_TLS .
func getCloudFlareTLSConfiguration() *tls.Config {
	return &tls.Config{
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.CurveP384, tls.CurveP521, tls.X25519},
	}
}