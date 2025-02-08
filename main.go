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
				ArgsUsage: "<url|filepath>",
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
				Usage: "Install a package",
				Description: "Installs a package from the given remote/local tarball or GitHub repository.\n" +
					"If this is a GitHub URL in the form https://github.com/user/repo, infpm will use the GitHub API to list the latest assets.\n" +
					"Otherwise, it will download a tarball directly from the given URL, or use a local file if -f is set.",
				Action: actionInstall,
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

	pm, err := NewPackageManager(PackageManagerOpts{StorePath: DEFAULT_STORE_PATH, Interactive: true})
	if err != nil {
		return err
	}

	opts := PreinstallPackageOpts{
		Name:    cmd.String("name"),
		Version: cmd.String("version"),
	}
	downloadUrl := reqPath
	var ppkg *PreinstallPackage

	if cmd.Bool("file") {
		opts.RetainTarball = true
		if ppkg, err = NewPackageFromFile(reqPath, opts); err != nil {
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

			opts.Name = asset.Name
			opts.Version = asset.Version
			downloadUrl = asset.Url
		}

		if ppkg, err = NewPackageFromRemote(downloadUrl, opts); err != nil {
			ppkg.Cleanup()
			return err
		}
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
