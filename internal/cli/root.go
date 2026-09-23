// Package cli builds a fresh command tree for each invocation. Only main owns
// process exit; commands return errors and use injected streams and adapters.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/myusuf3/commit/internal/app"
	"github.com/myusuf3/commit/internal/config"
	"github.com/myusuf3/commit/internal/git"
	"github.com/myusuf3/commit/internal/github"
	"github.com/myusuf3/commit/internal/httpapi"
	"github.com/myusuf3/commit/internal/llm"
	"github.com/spf13/cobra"
)

type Build struct{ Version, Revision, Date string }

type Options struct {
	In           io.Reader
	Out, Err     io.Writer
	Dir          string
	Interactive  bool
	Build        Build
	LoadConfig   func(string) (config.Config, error)
	NewGit       func(config.Config) app.Git
	NewGenerator func(config.Config) app.Generator
	NewHosting   func(config.Config, string) (app.Hosting, error)
	OpenBrowser  func(context.Context, string) error
}

type commandState struct {
	opts       Options
	reader     *bufio.Reader
	configPath string
	ctx        context.Context
}

func NewRoot(opts Options) *cobra.Command {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Build.Version == "" {
		opts.Build.Version = "dev"
	}
	if opts.LoadConfig == nil {
		opts.LoadConfig = config.Load
	}
	if opts.NewGit == nil {
		opts.NewGit = func(c config.Config) app.Git {
			d, _ := c.Duration()
			return &git.Client{Dir: opts.Dir, MaxDiffBytes: c.MaxDiffBytes, Timeout: d, Output: opts.Err}
		}
	}
	if opts.NewGenerator == nil {
		opts.NewGenerator = func(c config.Config) app.Generator {
			d, _ := c.Duration()
			return &llm.Client{HTTP: httpapi.NewClient(d), BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model, Provider: c.Provider, APIFormat: c.WireFormat(), Conventional: c.Conventional.TypeScopePrefix}
		}
	}
	if opts.NewHosting == nil {
		opts.NewHosting = func(c config.Config, remote string) (app.Hosting, error) {
			repo, err := github.ParseRemote(remote)
			if err != nil {
				return nil, err
			}
			d, _ := c.Duration()
			return &github.Client{HTTP: httpapi.NewClient(d), Token: c.GitHubToken, Repository: repo}, nil
		}
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
	s := &commandState{opts: opts, reader: bufio.NewReader(opts.In), ctx: context.Background()}
	root := &cobra.Command{
		Use: "commit", Short: "AI-powered Git commit and pull request assistant",
		SilenceErrors: true, SilenceUsage: true, Version: opts.Build.Version,
		Args: cobra.NoArgs,
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		s.ctx = cmd.Context()
		return s.ctx.Err()
	}
	root.SetIn(opts.In)
	root.SetOut(opts.Out)
	root.SetErr(opts.Err)
	root.SetVersionTemplate("commit version {{.Version}}\n")
	root.PersistentFlags().StringVar(&s.configPath, "config", "", "Configuration file (default: $XDG_CONFIG_HOME/commit/.commitrc or ~/.config/commit/.commitrc)")
	root.AddCommand(s.commitCommand(), s.prCommand(), s.initCommand(), s.updateCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print version and build information", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintf(opts.Out, "commit version %s\n  commit: %s\n  built:  %s\n", opts.Build.Version, opts.Build.Revision, opts.Build.Date)
		return err
	}})
	return root
}

func (s *commandState) service() (config.Config, app.Service, error) {
	c, err := s.opts.LoadConfig(s.configPath)
	if err != nil {
		return c, app.Service{}, err
	}
	if err := c.ValidateLLM(); err != nil {
		return c, app.Service{}, err
	}
	return c, app.Service{Git: s.opts.NewGit(c), Generator: s.opts.NewGenerator(c)}, nil
}

func openBrowser(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("browser opening is unsupported on %s", runtime.GOOS)
	}
	cmd.WaitDelay = time.Second
	return cmd.Run()
}
