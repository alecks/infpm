package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "infpm",
		Usage: "A minimal rootless package manager",
		Commands: []*cli.Command{
			{
				Name:    "install",
				Aliases: []string{"i"},
				Usage:   "install a package",
				Action:  actionInstall,
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		slog.Error("failed to run the top-level command", "err", err)
	}
}

func actionInstall(ctx context.Context, cmd *cli.Command) error {
	return nil
}
