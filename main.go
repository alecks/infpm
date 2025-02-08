package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"

	"github.com/urfave/cli/v3"
)

const DEFAULT_STORE_PATH = "./test/infpm"

// TODO: See if there's any more of these to add.
var alternativeArchKeywords = map[string]string{"darwin": "macos", "amd64": "x86"}

func main() {
	slogHdl := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(slogHdl))

	cmd := &cli.Command{
		Name:  "infpm",
		Usage: "A minimal rootless package manager",
		Commands: []*cli.Command{
			{
				Name:      "install",
				Aliases:   []string{"i"},
				ArgsUsage: "[url|filepath]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "file",
						Aliases: []string{"f"},
						Usage:   "Install a package from a local file.",
					},
					&cli.StringFlag{
						Name:    "name",
						Aliases: []string{"n"},
						Usage:   "Set the name of this package. Required if not using GitHub.",
					},
					&cli.StringFlag{
						Name:    "version",
						Aliases: []string{"v"},
						Usage:   "Set the version of this package. Required if not using GitHub.",
					},
				},
				Usage:       "install a package",
				Description: "Installs a package from the given URL. This can be a link to a GitHub repo, e.g. https://github.com/alecks/infpm, in which case it will download the latest release for your system. Otherwise, you can provide a specific URL or filepath for a tarball. Use the -f flag if providing a local file.",
				Action:      actionInstall,
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		slog.Error(err.Error())
	}
}

func actionInstall(ctx context.Context, cmd *cli.Command) error {
	reqPath := cmd.Args().Get(0)
	if reqPath == "" {
		return errors.New("A package URL or filepath (--file) is required. See --help install.")
	}

	pm, err := newPackageManager(DEFAULT_STORE_PATH, true)
	if err != nil {
		return err
	}

	name := cmd.String("name")
	version := cmd.String("version")
	downloadUrl := reqPath
	var ppkg *PreinstallPackage

	if cmd.Bool("file") {
		ppkg = &PreinstallPackage{RetainTarball: true}
		if err := ppkg.FromFile(cmd.Args().Get(0)); err != nil {
			ppkg.Cleanup()
			return err
		}
	} else {
		userUrl, err := url.ParseRequestURI(reqPath)
		if err != nil {
			slog.Error("The URL provided was invalid.")
			return err
		}
		if userUrl.Scheme != "http" && userUrl.Scheme != "https" {
			return errors.New("A non-http URL was provided. Please provide a URL with the scheme http:// or https://.")
		}

		if getGithubRepoName(userUrl) != "" {
			asset, err := fetchLatestGithubAsset(userUrl)
			if err != nil {
				slog.Error("failed to find asset from GitHub", "url", reqPath)
				return err
			}

			name = asset.Name
			version = asset.Version
			downloadUrl = asset.Url
		}

		ppkg = &PreinstallPackage{}
		if err := ppkg.FromRemote(downloadUrl); err != nil {
			ppkg.Cleanup()
			return err
		}
	}

	// TODO: reduce amount of ppkg.Cleanup calls. wish go had errdefer.
	if err := ppkg.Init(name, version); err != nil {
		ppkg.Cleanup()
		return err
	}

	pkg, err := pm.Install(ppkg)
	// Cleanup ASAP, don't defer.
	ppkg.Cleanup()
	if err != nil {
		slog.Error("installation failed", "package", ppkg.Name, "from", downloadUrl)
		return err
	}

	slog.Info("done", "path", pkg.Path)
	return nil
}
