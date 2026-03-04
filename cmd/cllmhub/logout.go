package main

import (
	"context"
	"fmt"
	"time"

	"github.com/oikosindex/cllmhub-cli/internal/auth"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke credentials and remove stored tokens",
	Long: `Revokes your refresh token on the cLLMHub server and deletes
the local credentials file at ~/.cllmhub/credentials.`,
	RunE: runLogout,
}

func runLogout(cmd *cobra.Command, args []string) error {
	// Attempt server-side revocation if we have a refresh token
	if refreshToken, err := auth.LoadRefreshToken(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := auth.RevokeToken(ctx, hubURL, refreshToken); err != nil {
			fmt.Printf("Warning: failed to revoke token server-side: %v\n", err)
		}
	}

	if err := auth.RemoveCredentials(); err != nil {
		return err
	}

	fmt.Println("Logged out. Credentials removed.")
	return nil
}
