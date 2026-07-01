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
CI hosts where no OS keychain is available).`,
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}

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
	if err := config.Save(cfg); err != nil {
		return err
	}
	if err := auth.StoreToken(token); err != nil {
		return err
	}

	fmt.Printf("\nSaved.\n  config: %s\n  token:  %s (0600)\n", config.Path(), auth.TokenPath())
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token, err := auth.LoadToken()
	if err != nil {
		return err
	}

	if cfg.GitLabURL == "" {
		fmt.Println("Not configured yet. Run `glute auth` to get started.")
		return nil
	}
	fmt.Printf("Instance: %s\n", cfg.GitLabURL)

	if token == "" {
		fmt.Println("Token:    none — run `glute auth`")
		return nil
	}
	fmt.Printf("Token:    present (from %s)\n", auth.Source())

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
	if err := auth.DeleteToken(); err != nil {
		return err
	}
	fmt.Println("Removed the stored token. (Config is left intact.)")
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
