package agent

import (
	"errors"

	"chaosbot/internal/provider"
)

// HumanError translates a provider/agent error into a message
// suitable for displaying to the user. The original error
// details (e.g. "rate limit exceeded; upgrade to pay-as-you-go")
// are preserved when useful. Returns just the original error's
// string for unrecognized categories.
//
// This is the only place that knows how to phrase provider
// errors for end users; the CLI and tests should call this
// before printing.
func HumanError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, provider.ErrContextLength):
		return "context too long; type /reset to start a new session"
	case errors.Is(err, provider.ErrRateLimited):
		return "rate limited; please wait a moment and try again"
	case errors.Is(err, provider.ErrAuthFailed):
		return "authentication failed; check CHAOSBOT_API_KEY or provider.api_key"
	case errors.Is(err, provider.ErrServerError):
		return "provider server error; please try again later"
	case errors.Is(err, provider.ErrBadRequest):
		return "bad request (this may be a bug; please report): " + err.Error()
	case errors.Is(err, provider.ErrNetwork):
		return "network error: " + err.Error()
	default:
		return err.Error()
	}
}
