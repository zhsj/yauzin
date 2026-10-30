package gmail

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// logRequest reports one Gmail API call at DEBUG. endpoint is the API path
// without host or query: the query carries search terms and must not be
// logged. A failed round trip reports err instead of a status.
//
// The line also carries what the call cost and where the account's budget stood
// right after it paid: units, "quota" as used/capacity, and the wait the
// reservation took. A call that did not reserve anything of its own (an upload
// chunk) leaves those three fields out.
func logRequest(account, method, endpoint string, spend quotaSpend, resp *http.Response, err error) {
	attrs := make([]any, 0, 10)
	attrs = append(attrs, "account", logAccount(account), "method", method, "endpoint", endpoint)
	attrs = append(attrs, spend.logAttrs()...)
	if err != nil {
		slog.Debug("gmail request", append(attrs, "err", err)...)
		return
	}
	slog.Debug("gmail request", append(attrs, "status", resp.StatusCode)...)
}

// logAccount is the account label of a log line. The backend prefix repeats
// what the message already says, so only the address is kept; an empty ID
// (no stored account yet) is shown as "-".
func logAccount(id string) string {
	if id == "" {
		return "-"
	}
	return strings.TrimPrefix(id, string(mail.KindGmail)+":")
}
