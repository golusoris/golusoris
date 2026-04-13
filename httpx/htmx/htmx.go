// Package htmx provides helpers for HTMX request detection + response
// headers. Stateless — no fx wiring needed.
//
// HTMX request headers: https://htmx.org/reference/#request_headers
// HTMX response headers: https://htmx.org/reference/#response_headers
package htmx

import "net/http"

// Request header names HTMX sets on its XHRs.
const (
	HeaderRequest        = "HX-Request"
	HeaderTrigger        = "HX-Trigger"
	HeaderTriggerName    = "HX-Trigger-Name"
	HeaderTarget         = "HX-Target"
	HeaderCurrentURL     = "HX-Current-URL"
	HeaderPrompt         = "HX-Prompt"
	HeaderBoosted        = "HX-Boosted"
	HeaderHistoryRestore = "HX-History-Restore-Request"
)

// Response header names HTMX reads from server replies.
const (
	ResponseLocation           = "HX-Location"
	ResponsePushURL            = "HX-Push-Url"
	ResponseRedirect           = "HX-Redirect"
	ResponseRefresh            = "HX-Refresh"
	ResponseReplaceURL         = "HX-Replace-Url"
	ResponseReswap             = "HX-Reswap"
	ResponseRetarget           = "HX-Retarget"
	ResponseReselect           = "HX-Reselect"
	ResponseTrigger            = "HX-Trigger"
	ResponseTriggerAfterSwap   = "HX-Trigger-After-Swap"
	ResponseTriggerAfterSettle = "HX-Trigger-After-Settle"
)

// IsRequest reports whether r was issued by HTMX (HX-Request: true).
func IsRequest(r *http.Request) bool { return r.Header.Get(HeaderRequest) == "true" }

// IsBoosted reports whether r comes from an hx-boost link (HX-Boosted: true).
func IsBoosted(r *http.Request) bool { return r.Header.Get(HeaderBoosted) == "true" }

// PushURL tells the browser to push the given URL to history.
func PushURL(w http.ResponseWriter, url string) { w.Header().Set(ResponsePushURL, url) }

// Redirect causes HTMX to perform a client-side redirect.
func Redirect(w http.ResponseWriter, url string) { w.Header().Set(ResponseRedirect, url) }

// Refresh tells HTMX to do a full page refresh.
func Refresh(w http.ResponseWriter) { w.Header().Set(ResponseRefresh, "true") }

// ReplaceURL replaces the current URL in the browser.
func ReplaceURL(w http.ResponseWriter, url string) { w.Header().Set(ResponseReplaceURL, url) }

// Reswap overrides the hx-swap value for this response.
func Reswap(w http.ResponseWriter, strategy string) { w.Header().Set(ResponseReswap, strategy) }

// Retarget overrides the hx-target value for this response.
func Retarget(w http.ResponseWriter, selector string) { w.Header().Set(ResponseRetarget, selector) }

// Trigger fires a named client-side event after the swap completes.
// Accepts simple event names (e.g. "dataChanged") or a JSON payload for
// events with data.
func Trigger(w http.ResponseWriter, event string) { w.Header().Set(ResponseTrigger, event) }
