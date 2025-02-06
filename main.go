package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// TODO: See if there's any more of these to add.
var alternativeArchKeywords = map[string]string{"darwin": "macos", "amd64": "x86"}

func main() {
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
						Usage:   "install a package from a local file",
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
	if cmd.Bool("file") {
		return errors.New("file unimplemented")
	} else {
		// assume URL
		userUrlRaw := cmd.Args().Get(0)
		if userUrlRaw == "" {
			return cli.Exit("A package URL is required. See --help install.", 1)
		}

		userUrl, err := url.ParseRequestURI(userUrlRaw)
		if err != nil {
			fmt.Println("The URL provided was invalid.")
			return err
		}
		if userUrl.Scheme != "http" && userUrl.Scheme != "https" {
			fmt.Println("The URL provided must be either http or https.")
			return errors.New("user provided a non-http URL")
		}

		tarballUrl, err := getFullGithubUrl(userUrl)
		if err != nil {
			return err
		}
		return installPackage(tarballUrl)
	}
}

type githubApiReleases struct {
	HtmlUrl string                   `json:"html_url"`
	Name    string                   `json:"name"`
	Assets  []*githubApiReleaseAsset `json:"assets"`
}

type githubApiReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadUrl string `json:"browser_download_url"`
}

func getFullGithubUrl(u *url.URL) (string, error) {
	splitPath := strings.Split(u.Path, "/")
	if u.Hostname() == "github.com" && len(splitPath)-1 == 2 {
		// We have a URL in the form github.com/user/repo.
		apiUrl, _ := url.Parse("https://api.github.com/repos")
		apiUrl = apiUrl.JoinPath(splitPath...).JoinPath("releases/latest")

		resp, err := http.Get(apiUrl.String())
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var releaseData githubApiReleases
		if err := json.NewDecoder(resp.Body).Decode(&releaseData); err != nil {
			slog.Error("failed to decode GitHub releases/latest API response")
			return "", err
		}

		fmt.Println("Found latest release: " + releaseData.Name + ". Read about this release: " + releaseData.HtmlUrl)

		// We want an asset that matches the OS and architecture. Sometimes 'macos' will be used instead of 'darwin', etc, so handle this here.
		wantedKeywords := []string{runtime.GOOS, runtime.GOARCH, alternativeArchKeywords[runtime.GOOS], alternativeArchKeywords[runtime.GOARCH]}
		var potentialAssets []*githubApiReleaseAsset

		for _, asset := range releaseData.Assets {
			kwCount := 0
			for _, kw := range wantedKeywords {
				// We want at least two keywords, i.e. one for arch and one for OS.
				if kwCount >= 2 {
					potentialAssets = append(potentialAssets, asset)
				}

				if strings.Contains(strings.ToLower(asset.Name), kw) {
					kwCount++
				}
			}
		}

		fmt.Println("The following assets were found that match your operating system and architecture:")
		for i, asset := range potentialAssets {
			fmt.Println(strconv.Itoa(i) + ") " + asset.Name)
		}

		// TODO: Helper function for things like this (there will be a few). Currently panics on non-number input.
		// TODO: Allow choosing assets outwith the guessed potential assets.
		chosenAssetIdx := -1
		for chosenAssetIdx >= len(potentialAssets) || chosenAssetIdx < 0 {
			fmt.Printf("Please choose an asset to install: ")
			_, err = fmt.Scanln(&chosenAssetIdx)
			if err != nil {
				panic(err)
			}
		}

		return potentialAssets[chosenAssetIdx].BrowserDownloadUrl, nil
	} else {
		// We don't care if the URL is right or not, we only care about helping expand repo URLs into release URLs.
		return u.String(), nil
	}
}

func installPackage(downloadUrl string) error {
	fmt.Println("Downloading " + downloadUrl)
	return errors.New("unimplemented")
}
