package notify

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Module provides a *Notifier to the fx graph, wired with the default
// sender selected by config. Out of the box it supports the SMTP sender;
// HTTP/webhook providers (resend, slack, discord, …) live in their own
// subpackages with separate import graphs, so apps add them explicitly
// with [WithSender] (see the extension point below).
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    notify.Module, // provides *notify.Notifier
//	)
//
// To add a non-default sender, supply it via fx and append in an invoke:
//
//	fx.New(
//	    golusoris.Core,
//	    notify.Module,
//	    fx.Invoke(func(n *notify.Notifier, s *slack.Sender) {
//	        notify.WithSender(s)(n)
//	    }),
//	)
//
// Config key prefix: notify.*
//
//	notify.sender = "smtp"   # default sender backend (only "smtp" is built-in)
//	notify.smtp.host = "..." # SMTP sender config (see [SMTPOptions])
var Module = fx.Module("golusoris.notify",
	fx.Provide(loadOptions),
	fx.Provide(newNotifier),
)

// Options selects and configures the default sender wired by [Module].
type Options struct {
	// Sender names the default sender backend. Only "smtp" is built-in;
	// other providers ship as notify/<provider> subpackages and are added
	// by the app via [WithSender]. Default "smtp".
	Sender string `koanf:"sender"`
	// SMTP configures the SMTP sender (used when Sender == "smtp").
	SMTP SMTPOptions `koanf:"smtp"`
}

func defaultOptions() Options {
	return Options{Sender: "smtp"}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("notify", &opts); err != nil {
		return Options{}, fmt.Errorf("notify: load options: %w", err)
	}
	return opts, nil
}

// newNotifier builds a *Notifier holding the configured default sender.
func newNotifier(opts Options, logger *slog.Logger) (*Notifier, error) {
	sender, err := buildSender(opts)
	if err != nil {
		return nil, err
	}
	logger.Debug("notify: started", slog.String("sender", sender.Name()))
	return New(logger, WithSender(sender)), nil
}

// LoadOptionsForTest exposes loadOptions for tests without fx.
func LoadOptionsForTest(cfg *config.Config) (Options, error) { return loadOptions(cfg) }

// NewNotifierForTest exposes newNotifier for tests without fx.
func NewNotifierForTest(opts Options, logger *slog.Logger) (*Notifier, error) {
	return newNotifier(opts, logger)
}

// buildSender constructs the default sender named by opts.Sender. Only
// "smtp" is built-in; any other name is an error pointing the app at the
// [WithSender] extension point, since HTTP providers live in subpackages
// with their own import graphs and cannot be selected by string here.
func buildSender(opts Options) (Sender, error) {
	switch opts.Sender {
	case "smtp", "":
		s, err := NewSMTPSender(opts.SMTP)
		if err != nil {
			return nil, fmt.Errorf("notify: build smtp sender: %w", err)
		}
		return s, nil
	default:
		return nil, fmt.Errorf(
			"notify: unknown default sender %q: only %q is built-in; add %s/%s via notify.WithSender",
			opts.Sender, "smtp", "notify", opts.Sender,
		)
	}
}
