//go:build integration

package auth

// SetExchangeBeforeSession installs the exchange's test hook (exchangeBeforeSession) and returns a func that removes
// it. Integration tests only; the tests of this package do not run in parallel.
func SetExchangeBeforeSession(f func()) (restore func()) {
	exchangeBeforeSession = f
	return func() { exchangeBeforeSession = nil }
}
