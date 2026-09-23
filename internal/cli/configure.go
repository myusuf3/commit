package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/myusuf3/commit/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (s *commandState) line(prompt string) (string, error) {
	if _, err := fmt.Fprint(s.opts.Err, prompt); err != nil {
		return "", err
	}
	line, err := s.readInput(func() (string, error) { return s.reader.ReadString('\n') })
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", errors.New("input closed; operation cancelled")
		}
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (s *commandState) secret(prompt string) (string, error) {
	if f, ok := s.opts.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if _, err := fmt.Fprint(s.opts.Err, prompt); err != nil {
			return "", err
		}
		state, err := term.GetState(int(f.Fd()))
		if err != nil {
			return "", err
		}
		defer term.Restore(int(f.Fd()), state)
		value, err := s.readInput(func() (string, error) {
			value, err := term.ReadPassword(int(f.Fd()))
			return string(value), err
		})
		fmt.Fprintln(s.opts.Err)
		if err != nil {
			return "", errors.New("unable to read secret; operation cancelled")
		}
		return strings.TrimSpace(value), nil
	}
	return s.line(prompt)
}

// A terminal read must not keep the process alive after Ctrl-C. The buffered
// result lets a cancelled reader finish without blocking a sender.
func (s *commandState) readInput(read func() (string, error)) (string, error) {
	type result struct {
		text string
		err  error
	}
	ready := make(chan result, 1)
	go func() { text, err := read(); ready <- result{text, err} }()
	select {
	case <-s.ctx.Done():
		return "", s.ctx.Err()
	case result := <-ready:
		return result.text, result.err
	}
}

func (s *commandState) initCommand() *cobra.Command {
	return &cobra.Command{Use: "init", Short: "Initialize user configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Path(s.configPath)
			if err != nil {
				return err
			}
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("configuration already exists at %s; edit it directly", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if !s.opts.Interactive {
				return errors.New("init requires a terminal; copy .commitrc.example or configure environment variables instead")
			}
			provider, err := s.line("LLM provider (openai, opencode-go) [openai]: ")
			if err != nil {
				return err
			}
			if provider == "" {
				provider = "openai"
			}
			if provider != "openai" && provider != "opencode-go" {
				return errors.New("provider must be openai or opencode-go")
			}
			c := config.DefaultForProvider(provider)
			if key, _ := config.APIKeyFromEnv(provider); key == "" {
				c.APIKey, err = s.secret("API key (blank to use an environment variable later): ")
				if err != nil {
					return err
				}
			}
			model, err := s.line("Model [" + c.Model + "]: ")
			if err != nil {
				return err
			}
			if model != "" {
				c.Model = model
			}
			if os.Getenv("COMMIT_GITHUB_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
				c.GitHubToken, err = s.secret("GitHub token (optional; needed only for PRs): ")
				if err != nil {
					return err
				}
			}
			conventional, err := s.line("Use conventional commits? [Y/n]: ")
			if err != nil {
				return err
			}
			switch strings.ToLower(conventional) {
			case "", "y", "yes":
				c.Conventional.TypeScopePrefix = true
			case "n", "no":
				c.Conventional.TypeScopePrefix = false
			default:
				return errors.New("expected yes or no")
			}
			if err := config.Write(path, c); err != nil {
				return err
			}
			_, err = fmt.Fprintf(s.opts.Out, "Configuration created at %s\n", path)
			return err
		}}
}
