package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// githubApiReleases represents the response from the GitHub API specified here:
// https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28#get-the-latest-releasetype
type githubApiReleases struct {
	HtmlUrl string                   `json:"html_url"`
	Name    string                   `json:"name"`
	Assets  []*githubApiReleaseAsset `json:"assets"`
	TagName string                   `json:"tag_name"`
}

// githubApiReleaseAsset is a member of the list of assets returned by the GitHub API specified here:
// https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28#get-the-latest-releasetype
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

type fetchedGithubAsset struct {
	Name    string
	Version string
	Url     string
}

// fetchLatestGithubAsset fetches the latest asset that suits the OS from GitHub, based on the URL.
// TODO: rework this entire thing to be non-interactive, with an interactive version
func fetchLatestGithubAsset(u *url.URL) (*fetchedGithubAsset, error) {
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

	return &fetchedGithubAsset{
		Name:    repoName,
		Version: releaseData.TagName,
		Url:     potentialAssets[chosenAssetIdx].BrowserDownloadUrl,
	}, nil
}

var idLetters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ123456789")

// generateId creates a new 5 character ID, suitable for file names. This is short -- it isn't designed to always
// be entirely unique as it should be paired with a package name and version.
func generateId() string {
	b := make([]rune, 5)
	for i := range b {
		b[i] = idLetters[rand.Intn(len(idLetters))]
	}
	return string(b)
}

func tarExtract(from io.Reader, to string) error {
	cmd := exec.Command("tar", "-xC", to)
	cmd.Stdin = from
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		slog.Error("failed to extract archive with tar", "args", cmd.Args)
		return err
	}

	return nil
}
