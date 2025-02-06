package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
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

	if cmd.Bool("file") {
		pkg, err := packageWithCmdMetadata(cmd)
		if err != nil {
			return err
		}

		return pkg.From(cmd.Args().Get(0))
	} else {
		userUrl, err := url.ParseRequestURI(reqPath)
		if err != nil {
			slog.Error("The URL provided was invalid.")
			return err
		}
		if userUrl.Scheme != "http" && userUrl.Scheme != "https" {
			return errors.New("A non-http URL was provided. Please provide a URL with the scheme http:// or https://.")
		}

		var pkg *Package
		if getGithubRepoName(userUrl) != "" {
			pkg, err = packageFromGithub(userUrl)
		} else {
			pkg, err = packageWithCmdMetadata(cmd)
			if err != nil {
				return err
			}
			err = pkg.FromRemote(userUrl.String())
			defer pkg.CleanupRemote()
		}
		return err
	}
}

func packageWithCmdMetadata(cmd *cli.Command) (*Package, error) {
	name := cmd.String("name")
	version := cmd.String("version")
	if name == "" || version == "" {
		return nil, errors.New("A name and version must be provided. See --help install for more info.")
	}
	return &Package{
		Name:    name,
		Version: version,
	}
}

type githubApiReleases struct {
	HtmlUrl string                   `json:"html_url"`
	Name    string                   `json:"name"`
	Assets  []*githubApiReleaseAsset `json:"assets"`
	TagName string                   `json:"tag_name"`
}

type githubApiReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadUrl string `json:"browser_download_url"`
}

// getGithubRepoName returns the repo name if if the URL is in the form github.com/user/repo. Otherwise, returns "".
func getGithubRepoName(u *url.URL) string {
	splitPath := strings.Split(u.Path, "/")
	if u.Hostname() == "github.com" && len(splitPath)-1 == 2 {
		return splitPath[2]
	} else {
		return ""
	}
}

func packageFromGithub(u *url.URL) (*Package, error) {
	repoName := getGithubRepoName(u)
	if repoName == "" {
		return nil, errors.New("internal: provided URL was not in the form github.com/user/repo")
	}

	apiUrl, _ := url.Parse("https://api.github.com/repos")
	apiUrl = apiUrl.JoinPath(u.Path).JoinPath("releases/latest")

	resp, err := http.Get(apiUrl.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, errors.New("GitHub returned non-OK status code. This is likely due to a ratelimit imposed by the API. Provide the URL to the release tarball yourself.")
	}

	var releaseData githubApiReleases
	if err := json.NewDecoder(resp.Body).Decode(&releaseData); err != nil {
		slog.Error("failed to decode GitHub releases/latest API response")
		return nil, err
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

	pkg := &Package{
		Name:    repoName,
		Version: releaseData.TagName,
	}
	if err := pkg.FromRemote(potentialAssets[chosenAssetIdx].BrowserDownloadUrl); err != nil {
		return nil, err
	}
	return pkg, nil
}

var idLetters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ123456789")

// We just want a small ID. Package names will usually be unique anyway, just need something to distinguish them.
func generateId() string {
	b := make([]rune, 5)
	for i := range b {
		b[i] = idLetters[rand.Intn(len(idLetters))]
	}
	return string(b)
}

type Package struct {
	// ID is set by the From/FromRemote functions to uniquely identify a package.
	Id      string
	Name    string
	Version string
	// Path is the location of the installed package.
	Path string

	// tarballPath is the location of the tarball either provided or downloaded remotely.
	tarballPath string
}

// From installs a package from a local filepath.
func (p *Package) From(filepath string) error {
	p.tarballPath = filepath
	p.Id = p.Name + "-" + p.Version + "-" + generateId()

	return p.install()
}

// FromRemote downloads and installs a package from a remote URL.
// Expects Name and Version to be set.
func (p *Package) FromRemote(tarballUrl string) error {
	tarballPath, err := p.downloadRemote(tarballUrl)
	defer p.CleanupRemote()
	if err != nil {
		return err
	}
	return p.From(tarballPath)
}

func (p *Package) CleanupRemote() error {
	return os.Remove(p.tarballPath)
}

// install installs a package from tarballPath and sets Path to the location of the installed package.
func (p *Package) install() error {
	return errors.New("install unimplemented")
}

// downloadRemote downloads a tarball from a remote URL and returns the path to the temporary file.
func (p *Package) downloadRemote(tarballUrl string) (string, error) {
	tempFile, err := os.CreateTemp("", generateId()+path.Base(tarballUrl))
	if err != nil {
		slog.Error("failed to create temporary file for remote download")
		return "", err
	}

	resp, err := http.Get(tarballUrl)
	if err != nil {
		slog.Error("failed to download tarball from remote URL")
		return "", err
	}
	defer resp.Body.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		slog.Error("failed to write remote tarball to a temporary file")
		return "", err
	}

	return tempFile.Name(), nil
}
