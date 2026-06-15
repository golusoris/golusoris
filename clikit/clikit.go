// Package clikit provides a cobra + fx-aware CLI builder.
//
// It wires cobra's root command into the fx lifecycle so that:
//   - Long-running commands use fx.New(...).Run() naturally.
//   - One-shot commands skip the fx lifecycle entirely.
//   - The fx app starts only when the selected command opts in via [WithFx].
//
// Usage:
//
//	root := clikit.New("myapp", "My application")
//	root.AddCommand(
//	    clikit.Command("serve", "Start the HTTP server",
//	        clikit.WithFx(
//	            core.Module,
//	            httpx.Module,
//	        ),
//	        clikit.WithRun(func(lc fx.Lifecycle) {
//	            // lc.Append hooks here; fx.Run keeps the process alive
//	        }),
//	    ),
//	    clikit.Command("version", "Print version", clikit.WithRunE(func(cmd *cobra.Command, args []string) error {
//	        fmt.Fprintln(cmd.OutOrStdout(), version)
//	        return nil
//	    })),
//	)
//	root.Execute()
package clikit

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

// Root wraps a cobra root command.
type Root struct{ cmd *cobra.Command }

// New creates a new root command.
func New(use, short string) *Root {
	return &Root{cmd: &cobra.Command{Use: use, Short: short, SilenceUsage: true}}
}

// AddCommand adds one or more sub-commands to the root.
func (r *Root) AddCommand(cmds ...*cobra.Command) { r.cmd.AddCommand(cmds...) }

// Execute runs the root command. Call from main().
func (r *Root) Execute() error { return r.cmd.Execute() } //nolint:wrapcheck // cobra error already has context

// Cobra returns the underlying cobra.Command for advanced use.
func (r *Root) Cobra() *cobra.Command { return r.cmd }

// cmdOptions configures a sub-command.
type cmdOptions struct {
	fxOpts     []fx.Option
	runFx      func(*fx.App)
	runE       func(*cobra.Command, []string) error
	oneShotRun func(ctx context.Context) error
}

// Option configures a sub-command built with [Command].
type Option func(*cmdOptions)

// WithFx registers fx.Options that are passed to fx.New when the command runs.
// The fx app is started and then Run() is called (blocking until shutdown).
func WithFx(opts ...fx.Option) Option {
	return func(o *cmdOptions) { o.fxOpts = append(o.fxOpts, opts...) }
}

// WithRunHook is called with the started fx.App; useful for triggering a one-shot
// action after all components are started, then signalling shutdown.
// Leave nil to just call app.Run() (default long-running mode).
func WithRunHook(fn func(app *fx.App)) Option {
	return func(o *cmdOptions) { o.runFx = fn }
}

// WithRunE sets a plain cobra RunE handler (no fx). Mutually exclusive with [WithFx].
func WithRunE(fn func(cmd *cobra.Command, args []string) error) Option {
	return func(o *cmdOptions) { o.runE = fn }
}

// WithFxRun configures a ONE-SHOT command: the fx app is built, started (running
// all providers + any fx.Invoke/fx.Populate in opts), then run executes, then
// the app is stopped — WITHOUT calling app.Run()/blocking on a signal. The RunE
// returns errors.Join(runErr, stopErr) so the process exit code reflects
// failure. fx's event log is silenced by default (fx.NopLogger); a caller-
// supplied fx.WithLogger in opts overrides it.
//
// Access dependencies by including fx.Populate(&dep) in opts and capturing dep
// in the run closure (or do the work in an fx.Invoke whose error surfaces via
// Start). Mutually exclusive with [WithFx]/[WithRunE].
func WithFxRun(run func(ctx context.Context) error, opts ...fx.Option) Option {
	return func(o *cmdOptions) {
		o.fxOpts = append(o.fxOpts, opts...)
		o.oneShotRun = run
	}
}

// oneShotRunE builds the one-shot RunE: start → run → stop, no blocking.
func oneShotRunE(o *cmdOptions) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		ctx := c.Context()
		app := fx.New(append([]fx.Option{fx.NopLogger}, o.fxOpts...)...)
		if err := app.Err(); err != nil {
			return err //nolint:wrapcheck // fx error already carries context
		}
		startCtx, cancel := context.WithTimeout(ctx, app.StartTimeout())
		defer cancel()
		if err := app.Start(startCtx); err != nil {
			return fmt.Errorf("clikit: start: %w", err)
		}
		runErr := o.oneShotRun(ctx)
		stopCtx, stopCancel := context.WithTimeout(ctx, app.StopTimeout())
		defer stopCancel()
		stopErr := app.Stop(stopCtx)
		if stopErr != nil {
			stopErr = fmt.Errorf("clikit: stop: %w", stopErr)
		}
		return errors.Join(runErr, stopErr)
	}
}

// Command builds a cobra.Command from the given options.
func Command(use, short string, opts ...Option) *cobra.Command {
	o := &cmdOptions{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{Use: use, Short: short, SilenceUsage: true}

	switch {
	case o.runE != nil:
		cmd.RunE = o.runE
	case o.oneShotRun != nil:
		cmd.RunE = oneShotRunE(o)
	case len(o.fxOpts) > 0:
		cmd.RunE = func(c *cobra.Command, args []string) error {
			app := fx.New(o.fxOpts...)
			if app.Err() != nil {
				return app.Err()
			}
			if o.runFx != nil {
				o.runFx(app)
			} else {
				app.Run()
			}
			return nil
		}
	}

	return cmd
}
