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
	fxOpts []fx.Option
	runFx  func(*fx.App)
	runE   func(*cobra.Command, []string) error
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
