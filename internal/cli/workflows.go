package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/myusuf3/commit/internal/app"
	"github.com/spf13/cobra"
)

func (s *commandState) canRun(accept, dryRun bool) error {
	if !s.opts.Interactive && !accept && !dryRun {
		return errors.New("non-interactive input: use --auto-accept to apply changes or --dry-run to preview")
	}
	return nil
}

func (s *commandState) confirm(prompt string, accept bool) (bool, error) {
	if accept {
		return true, nil
	}
	answer, err := s.line(prompt + " [y/N]: ")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

func (s *commandState) commitCommand() *cobra.Command {
	var accept, dryRun bool
	cmd := &cobra.Command{Use: "commit", Short: "Generate a commit message from staged changes", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := s.canRun(accept, dryRun); err != nil {
				return err
			}
			_, service, err := s.service()
			if err != nil {
				return err
			}
			fmt.Fprintln(s.opts.Err, "Generating a commit message (staged diff is sent to the configured provider)...")
			plan, err := service.PrepareCommit(cmd.Context())
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(s.opts.Out, plan.Message); err != nil {
				return err
			}
			if dryRun {
				return nil
			}
			ok, err := s.confirm("Commit with this message?", accept)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(s.opts.Err, "Commit cancelled.")
				return nil
			}
			return service.Commit(cmd.Context(), plan)
		}}
	cmd.Flags().BoolVarP(&accept, "auto-accept", "y", false, "Accept the generated commit without prompting")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Generate and print only; do not commit")
	return cmd
}

func (s *commandState) prCommand() *cobra.Command {
	var accept, draft, dryRun, noBrowser bool
	var values []string
	cmd := &cobra.Command{Use: "pr", Short: "Create or update a GitHub pull request", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := s.canRun(accept, dryRun); err != nil {
				return err
			}
			issues, err := app.ParseIssues(values)
			if err != nil {
				return err
			}
			// Reject an explicitly empty flag as input validation. ParseIssues
			// returns nil for blank input, so omitted/interactive blank issues
			// still preserve existing links; they never represented an empty set.
			if cmd.Flags().Changed("issue") && len(issues) == 0 {
				return errors.New("--issue was provided without a value; pass a GitHub number or TEAM-123, or omit the flag")
			}
			if !cmd.Flags().Changed("issue") && s.opts.Interactive && !accept && !dryRun {
				line, err := s.line("Related issues (123, TEAM-456; blank keeps existing links): ")
				if err != nil {
					return err
				}
				issues, err = app.ParseIssues([]string{line})
				if err != nil {
					return err
				}
			}
			c, service, err := s.service()
			if err != nil {
				return err
			}
			if strings.TrimSpace(c.GitHubToken) == "" {
				return errors.New("GitHub token required: set GITHUB_TOKEN or github_token in the config")
			}
			remote, err := service.Git.RemoteURL(cmd.Context())
			if err != nil {
				return err
			}
			host, err := s.opts.NewHosting(c, remote)
			if err != nil {
				return err
			}
			fmt.Fprintln(s.opts.Err, "Generating a pull request (committed branch diff is sent to the configured provider)...")
			plan, err := service.PreparePR(cmd.Context(), host, issues, draft)
			if err != nil {
				return err
			}
			currentRemote, err := service.Git.RemoteURL(cmd.Context())
			if err != nil {
				return err
			}
			if currentRemote != remote {
				return errors.New("origin changed during preparation; run the command again")
			}
			if _, err := fmt.Fprintf(s.opts.Out, "Title: %s\n\n%s\n", plan.PR.Title, plan.PR.Body); err != nil {
				return err
			}
			action := "Create pull request"
			if plan.Draft {
				action = "Create draft pull request"
			}
			if plan.PR.Number > 0 {
				action = fmt.Sprintf("Update pull request #%d", plan.PR.Number)
				if draft {
					fmt.Fprintln(s.opts.Err, "--draft applies only to new pull requests; existing draft status is unchanged.")
				}
			}
			if plan.NeedsPush {
				action = "Push branch to origin and " + strings.ToLower(action)
			}
			fmt.Fprintf(s.opts.Err, "Plan: %s (%s -> %s).\n", action, plan.Branch, plan.Base)
			if dryRun {
				return nil
			}
			ok, err := s.confirm(action+"?", accept)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(s.opts.Err, "Pull request cancelled; nothing pushed or changed.")
				return nil
			}
			url, err := service.ApplyPR(cmd.Context(), host, plan)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(s.opts.Out, url); err != nil {
				return err
			}
			if s.opts.Interactive && !noBrowser {
				if err := s.opts.OpenBrowser(cmd.Context(), url); err != nil {
					fmt.Fprintln(s.opts.Err, "Could not open browser; use the URL above.")
				}
			}
			return nil
		}}
	cmd.Flags().StringSliceVarP(&values, "issue", "i", nil, "Related GitHub numbers or Linear keys (123,TEAM-456)")
	cmd.Flags().BoolVarP(&accept, "auto-accept", "y", false, "Accept the generated PR and any required push without prompting")
	cmd.Flags().BoolVarP(&draft, "draft", "d", false, "Create a draft pull request")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Generate and print only; do not push or modify a PR")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open the pull request in a browser")
	return cmd
}
