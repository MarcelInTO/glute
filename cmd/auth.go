package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MarcelInTO/glute/internal/auth"
	"github.com/MarcelInTO/glute/internal/config"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/spf13/cobra"
)

var authURL string

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Connect glute to your GitLab instance",
	Long: `auth prompts for a GitLab personal access token (scope: read_api),
validates it against your instance, and stores it in a 0600 file.

The token can also be supplied at runtime via the GLUTE_TOKEN environment
variable, which takes precedence over the stored file (handy on headless or
CI hosts where no OS keychain is available).

Use --instance/-i to set up a second GitLab server under its own profile.`,
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status and verify the token",
	RunE:  runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored token",
	RunE:  runAuthLogout,
}

func init() {
	authCmd.Flags().StringVar(&authURL, "url", "", "GitLab instance URL (skips the prompt)")
	authCmd.AddCommand(authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	instance, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(instance)
	if err != nil {
		return err
	}

	fmt.Printf("Instance: %s\n", instance)

	url := firstNonEmpty(authURL, cfg.GitLabURL)
	if url == "" {
		if url, err = promptLine("GitLab instance URL (e.g. https://gitlab.example.com): "); err != nil {
			return err
		}
	}
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" {
		return errors.New("a GitLab instance URL is required")
	}

	fmt.Printf("Create a token with the 'read_api' scope at:\n  %s/-/user_settings/personal_access_tokens\n\n", url)

	token, err := promptSecret("Personal access token: ")
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("a token is required")
	}

	fmt.Print("Validating... ")
	client, err := gitlab.NewClient(url, token, cfg.CACert)
	if err != nil {
		fmt.Println("failed")
		return err
	}
	username, err := client.WhoAmI(cmd.Context())
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("could not authenticate to %s: %w", url, err)
	}
	fmt.Printf("ok — authenticated as %s\n", username)

	cfg.GitLabURL = url
	if err := config.Save(instance, cfg); err != nil {
		return err
	}
	if err := auth.StoreToken(instance, token); err != nil {
		return err
	}

	fmt.Printf("\nSaved.\n  config: %s\n  token:  %s (0600)\n", config.Path(instance), auth.TokenPath(instance))
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	instance, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(instance)
	if err != nil {
		return err
	}
	token, err := auth.LoadToken(instance)
	if err != nil {
		return err
	}

	fmt.Printf("Instance: %s\n", instance)
	if cfg.GitLabURL == "" {
		fmt.Printf("Not configured yet. Run `glute auth%s` to get started.\n", instanceHint(instance))
		return nil
	}
	fmt.Printf("URL:      %s\n", cfg.GitLabURL)

	if token == "" {
		fmt.Printf("Token:    none — run `glute auth%s`\n", instanceHint(instance))
		return nil
	}
	fmt.Printf("Token:    present (from %s)\n", auth.Source(instance))

	fmt.Print("Checking... ")
	client, err := gitlab.NewClient(cfg.GitLabURL, token, cfg.CACert)
	if err != nil {
		fmt.Println("failed")
		return err
	}
	username, err := client.WhoAmI(cmd.Context())
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("token rejected by %s: %w", cfg.GitLabURL, err)
	}
	fmt.Printf("ok — authenticated as %s\n", username)
	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	instance, err := config.ResolveInstance(instanceFlag)
	if err != nil {
		return err
	}
	if err := auth.DeleteToken(instance); err != nil {
		return err
	}
	fmt.Printf("Removed the stored token for %q. (Config is left intact.)\n", instance)
	if strings.TrimSpace(os.Getenv("GLUTE_TOKEN")) != "" {
		fmt.Println("Note: GLUTE_TOKEN is still set in your environment and will still be used.")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
