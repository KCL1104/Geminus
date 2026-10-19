package auth

// Refreshing reports whether a JWKS fetch is in flight, so a test can wait
// for a background refresh to finish (store its keys or its error) before
// it moves the clock on.
func (v *OIDCVerifier) Refreshing() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.refreshing != nil
}
