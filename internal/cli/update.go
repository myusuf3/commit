package cli

import (
	"errors"
	"fmt"

	"github.com/myusuf3/commit/internal/httpapi"
	"github.com/myusuf3/commit/internal/update"
	"github.com/spf13/cobra"
)

func (s *commandState) updateCommand() *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{Use: "update", Short: "Update from the configured GitHub release repository", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := s.opts.LoadConfig(s.configPath)
			if err != nil {
				return err
			}
			d, err := c.Duration()
			if err != nil {
				return err
			}
			httpClient := httpapi.NewClient(d)
			if s.opts.UpdateTransport != nil {
				httpClient.Transport = s.opts.UpdateTransport
			}
			client := &update.Client{HTTP: httpClient, Repository: c.ReleaseRepository, Token: c.GitHubToken, Current: s.opts.Build.Version}
			plan, err := client.Check(cmd.Context(), force)
			// A development build cannot be compared, but --check can still
			// report what the latest release is.
			var dev *update.DevelopmentBuildError
			if check && errors.As(err, &dev) {
				_, err = fmt.Fprintf(s.opts.Out, "Latest release: %s (current: development build). Run 'commit update --force' to install it.\n", dev.Latest)
				return err
			}
			if err != nil {
				return err
			}
			if plan == nil {
				_, err = fmt.Fprintf(s.opts.Out, "Already up to date (%s).\n", s.opts.Build.Version)
				return err
			}
			fmt.Fprintf(s.opts.Out, "Update available: %s (current: %s)\n", plan.Version, s.opts.Build.Version)
			if check {
				return nil
			}
			if err := client.Install(cmd.Context(), *plan); err != nil {
				return err
			}
			_, err = fmt.Fprintf(s.opts.Out, "Updated to %s.\n", plan.Version)
			return err
		}}
	cmd.Flags().BoolVarP(&check, "check", "c", false, "Check for updates without installing")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Install the latest release even for a development or newer build")
	return cmd
}
