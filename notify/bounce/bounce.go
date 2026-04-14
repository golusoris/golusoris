// Package bounce handles email bounce and complaint webhooks from
// SES (via SNS) and Postmark. Handlers parse the provider-specific
// payload, classify the event, and forward it to a [Handler] closure
// so apps can add the affected address to their suppression list
// ([notify/unsub]) or take other action.
//
// Usage:
//
//	h := bounce.Handler(func(ctx context.Context, ev bounce.Event) {
//	    if ev.Permanent() {
//	        _ = unsubSvc.Store.Add(ctx, ev.Email)
//	    }
//	})
//	mux.Handle("/webhooks/ses",      bounce.SES(h))
//	mux.Handle("/webhooks/postmark", bounce.Postmark(h))
package bounce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Kind describes the event type.
type Kind string

const (
	// KindBounce is a hard bounce (invalid recipient, mailbox full, …).
	KindBounce Kind = "bounce"
	// KindComplaint is a spam complaint (recipient marked mail as spam).
	KindComplaint Kind = "complaint"
	// KindDelivery is a successful delivery (SES-only; Postmark omits).
	KindDelivery Kind = "delivery"
)

// Event is a normalized bounce/complaint event.
type Event struct {
	// Kind distinguishes bounce / complaint / delivery.
	Kind Kind
	// Email is the affected recipient.
	Email string
	// MessageID is the upstream provider's message identifier.
	MessageID string
	// Subtype is the provider-specific reason code
	// (e.g. "Permanent", "General" for SES; "HardBounce",
	// "SpamComplaint" for Postmark).
	Subtype string
	// Permanent reports whether the bounce is permanent (add to
	// suppression list) or transient (retry later). Always true for
	// complaints.
	PermanentFlag bool
	// Reason is the provider's free-text diagnostic.
	Reason string
	// Timestamp is when the provider recorded the event.
	Timestamp time.Time
	// Provider names the origin ("ses" | "postmark").
	Provider string
}

// Permanent returns true when the event should trigger suppression.
func (e Event) Permanent() bool { return e.PermanentFlag }

// HandlerFunc receives parsed events.
type HandlerFunc func(ctx context.Context, ev Event)

// maxBodyBytes caps webhook request bodies.
const maxBodyBytes = 1 << 20 // 1 MiB

// SES returns an http.Handler that accepts AWS SES bounce/complaint
// notifications delivered via SNS. SNS "SubscriptionConfirmation"
// messages are acknowledged by fetching their SubscribeURL (see
// https://docs.aws.amazon.com/sns/latest/dg/sns-message-and-json-formats.html).
//
// This handler verifies only the JSON shape; SNS signature verification
// is outside scope — mount behind [webhooks/in] or an SNS-aware proxy
// when exposed publicly.
func SES(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "body error", http.StatusBadRequest)
			return
		}
		var env snsEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		switch env.Type {
		case "SubscriptionConfirmation":
			// App must visit SubscribeURL; we don't do it automatically to
			// keep the handler side-effect-free. Accept with 200.
			w.WriteHeader(http.StatusOK)
			return
		case "Notification":
			// Continue below.
		default:
			http.Error(w, "unknown sns type", http.StatusBadRequest)
			return
		}
		if err := dispatchSESNotification(r.Context(), env.Message, h); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func dispatchSESNotification(ctx context.Context, raw string, h HandlerFunc) error {
	var n sesNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return fmt.Errorf("notify/bounce: ses message: %w", err)
	}
	switch n.NotificationType {
	case "Bounce":
		for _, r := range n.Bounce.BouncedRecipients {
			h(ctx, Event{
				Kind:          KindBounce,
				Email:         r.EmailAddress,
				MessageID:     n.Mail.MessageID,
				Subtype:       n.Bounce.BounceType,
				PermanentFlag: n.Bounce.BounceType == "Permanent",
				Reason:        r.DiagnosticCode,
				Timestamp:     n.Bounce.Timestamp,
				Provider:      "ses",
			})
		}
	case "Complaint":
		for _, r := range n.Complaint.ComplainedRecipients {
			h(ctx, Event{
				Kind:          KindComplaint,
				Email:         r.EmailAddress,
				MessageID:     n.Mail.MessageID,
				Subtype:       n.Complaint.ComplaintFeedbackType,
				PermanentFlag: true,
				Timestamp:     n.Complaint.Timestamp,
				Provider:      "ses",
			})
		}
	case "Delivery":
		for _, addr := range n.Delivery.Recipients {
			h(ctx, Event{
				Kind:      KindDelivery,
				Email:     addr,
				MessageID: n.Mail.MessageID,
				Timestamp: n.Delivery.Timestamp,
				Provider:  "ses",
			})
		}
	default:
		return fmt.Errorf("notify/bounce: unknown ses type %q", n.NotificationType)
	}
	return nil
}

// Postmark returns an http.Handler that accepts Postmark bounce
// webhook payloads. Postmark POSTs JSON directly (no wrapper).
// See https://postmarkapp.com/developer/webhooks/bounce-webhook.
func Postmark(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "body error", http.StatusBadRequest)
			return
		}
		var p postmarkBounce
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if p.Email == "" {
			http.Error(w, "missing email", http.StatusBadRequest)
			return
		}
		kind := KindBounce
		if p.Type == "SpamComplaint" {
			kind = KindComplaint
		}
		h(r.Context(), Event{
			Kind:          kind,
			Email:         p.Email,
			MessageID:     p.MessageID,
			Subtype:       p.Type,
			PermanentFlag: !p.CanActivate || p.Type == "SpamComplaint" || p.Type == "HardBounce",
			Reason:        p.Description,
			Timestamp:     p.BouncedAt,
			Provider:      "postmark",
		})
		w.WriteHeader(http.StatusOK)
	})
}

func readBody(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("notify/bounce: read body: %w", err)
	}
	if len(b) == 0 {
		return nil, errors.New("notify/bounce: empty body")
	}
	return b, nil
}

// snsEnvelope is the outer SNS delivery envelope. See
// https://docs.aws.amazon.com/sns/latest/dg/sns-message-and-json-formats.html.
type snsEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

// sesNotification is the inner SES payload carried by SNS.Message.
type sesNotification struct {
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID string `json:"messageId"`
	} `json:"mail"`
	Bounce struct {
		BounceType        string    `json:"bounceType"`
		Timestamp         time.Time `json:"timestamp"`
		BouncedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			DiagnosticCode string `json:"diagnosticCode"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint struct {
		ComplaintFeedbackType string    `json:"complaintFeedbackType"`
		Timestamp             time.Time `json:"timestamp"`
		ComplainedRecipients  []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
	} `json:"complaint"`
	Delivery struct {
		Timestamp  time.Time `json:"timestamp"`
		Recipients []string  `json:"recipients"`
	} `json:"delivery"`
}

// postmarkBounce mirrors Postmark's bounce webhook payload.
type postmarkBounce struct {
	MessageID   string    `json:"MessageID"`
	Type        string    `json:"Type"`
	Email       string    `json:"Email"`
	Description string    `json:"Description"`
	CanActivate bool      `json:"CanActivate"`
	BouncedAt   time.Time `json:"BouncedAt"`
}
