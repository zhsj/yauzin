package gmail

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// apiFailure is one rejected API call. It keeps the status and the reason the
// retry layer reads, and unwraps to the category the domain layer reports.
type apiFailure struct {
	op string
	// subject names the object of the call, such as a thread or label ID. It is
	// empty for a call that addresses the mailbox as a whole.
	subject string
	status  int
	reason  string
	detail  string
	kind    error
}

// Error renders the call the way the package always reported it, naming the
// subject when the call had one.
func (e *apiFailure) Error() string {
	op := e.op
	if e.subject != "" {
		op += " " + e.subject
	}
	if e.detail == "" {
		return fmt.Sprintf("gmail %s: %s", op, http.StatusText(e.status))
	}
	return fmt.Sprintf("gmail %s: %s: %s", op, http.StatusText(e.status), e.detail)
}

// Unwrap exposes the category to errors.Is.
func (e *apiFailure) Unwrap() error { return e.kind }

// apiError maps a non-200 response onto the mail error categories. The body of
// a Google error carries a human readable message, which is kept as detail for
// anything that is not a category.
func apiError(op string, resp *http.Response) error {
	return apiErrorFor(op, "", resp)
}

// apiErrorFor is apiError for a call that names its object, such as one
// embedded in a batch: subject keeps a rejection attributable to the row it
// belongs to, and the returned error carries the same category, so the retry
// layer can tell a rate limited part from a broken one.
func apiErrorFor(op, subject string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var payload struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	failure := &apiFailure{
		op:      op,
		subject: subject,
		status:  resp.StatusCode,
		detail:  payload.Error.Message,
	}
	if failure.detail == "" {
		failure.detail = strings.TrimSpace(string(body))
	}
	for _, e := range payload.Error.Errors {
		if e.Reason != "" {
			failure.reason = e.Reason
			break
		}
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		failure.kind = mail.ErrNotFound
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		failure.kind = mail.ErrInvalidArgument
	case http.StatusUnauthorized:
		failure.kind = mail.ErrUnauthorized
	case http.StatusForbidden:
		// A token granted before a scope was added answers
		// 403 insufficientPermissions: the credential still works, the grant
		// does not, and only a new sign-in widens it. Rate limiting is the
		// other 403 worth naming; anything else (a disabled account) stays
		// uncategorised so the UI does not ask for a pointless sign-in.
		switch {
		case failure.reason == "insufficientPermissions":
			failure.kind = mail.ErrUnauthorized
		case isRateLimitReason(failure.reason) || payload.Error.Status == "RESOURCE_EXHAUSTED":
			failure.kind = mail.ErrRateLimited
		}
	case http.StatusTooManyRequests:
		failure.kind = mail.ErrRateLimited
	}
	return failure
}

// isRateLimitReason reports whether a Google error reason names rate limiting.
func isRateLimitReason(reason string) bool {
	switch reason {
	case "rateLimitExceeded", "userRateLimitExceeded", "dailyLimitExceeded":
		return true
	}
	return false
}
