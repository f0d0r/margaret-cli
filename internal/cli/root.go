package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// version is injected at build time via
// -ldflags "-X github.com/f0d0r/margaret-cli/internal/cli.version=...".
// It defaults to "dev" for plain `go build` runs.
var version = "dev"

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:           "margaret",
	Short:         "Scan ebook collections and search book metadata",
	Version:       version,
	SilenceErrors: true,
	SilenceUsage:  true,
	Long: `Scans a directory tree for ebook files, reads their author and title
metadata, and reports the results. Ebooks are found both directly on disk
and inside archives (nested archives are unpacked up to a configurable
depth), and scan results are kept in a SQLite database that can be
regenerated into JSON reports or searched without rescanning.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cobra.CheckErr(rootCmd.ExecuteContext(ctx))
}
