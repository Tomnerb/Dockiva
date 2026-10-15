package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

//go:embed VERSION
var sourceVersion string

// These values are overridden with -ldflags for release builds. Keeping the
// version embedded means ordinary `go build` and developer builds still report
// the correct product version.
var (
	buildVersion = ""
	buildCommit  = "development"
	buildDate    = ""
)

type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	Channel   string `json:"channel"`
}

type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Available      bool   `json:"available"`
	ReleaseName    string `json:"releaseName"`
	ReleaseURL     string `json:"releaseUrl"`
	PublishedAt    string `json:"publishedAt"`
	Notes          string `json:"notes"`
	Message        string `json:"message"`
	ArtifactName   string `json:"artifactName"`
	ArtifactSize   int64  `json:"artifactSize"`
}

func currentVersion() string {
	if version := strings.TrimSpace(buildVersion); version != "" {
		return strings.TrimPrefix(version, "v")
	}
	return strings.TrimPrefix(strings.TrimSpace(sourceVersion), "v")
}

func (s *AppService) GetVersionInfo() VersionInfo {
	channel := "stable"
	if strings.Contains(currentVersion(), "-") {
		channel = "prerelease"
	}
	return VersionInfo{
		Version:   currentVersion(),
		Commit:    strings.TrimSpace(buildCommit),
		BuildDate: strings.TrimSpace(buildDate),
		Channel:   channel,
	}
}

// coreUpdater is the updater of the headless core (dockiva --core), which runs
// without a Wails app; the native macOS front end updates through it.
var coreUpdater *updater.Updater

// appUpdater is the Wails app's updater, or the core's.
func appUpdater() *updater.Updater {
	if app := application.Get(); app != nil && app.Updater != nil {
		return app.Updater
	}
	return coreUpdater
}

// updaterConfig is how Dockiva finds and verifies releases: GitHub releases with
// a required SHA256SUMS entry for the platform's archive.
func updaterConfig() (updater.Config, error) {
	githubUpdates, err := github.New(github.Config{
		Repository:    "Tomnerb/Dockiva",
		ChecksumAsset: "SHA256SUMS",
	})
	if err != nil {
		return updater.Config{}, err
	}
	return updater.Config{
		CurrentVersion: currentVersion(),
		Providers:      []updater.Provider{requiredChecksumProvider{provider: githubUpdates}},
		Window:         updater.WindowNone,
	}, nil
}

func (s *AppService) CheckForUpdates() (UpdateInfo, error) {
	current := currentVersion()
	u := appUpdater()
	if u == nil {
		return UpdateInfo{}, errors.New("application updater is unavailable")
	}

	release, err := u.Check(context.Background())
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("check for updates: %w", err)
	}
	if release == nil {
		return UpdateInfo{
			CurrentVersion: current,
			LatestVersion:  current,
			Message:        "Dockiva is up to date.",
		}, nil
	}

	return updateInfoFromRelease(current, release)
}

// ApplyUpdate downloads the platform-specific release, verifies its SHA-256
// digest, stages it, and restarts Dockiva through Wails' detached updater
// helper. The running application is only replaced after it has exited.
func (s *AppService) ApplyUpdate() error {
	u := appUpdater()
	if u == nil {
		return errors.New("application updater is unavailable")
	}
	if err := u.DownloadAndInstall(context.Background()); err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	if err := u.Restart(context.Background()); err != nil {
		return fmt.Errorf("restart into update: %w", err)
	}
	return nil
}

func updateInfoFromRelease(current string, release *updater.Release) (UpdateInfo, error) {
	if release == nil {
		return UpdateInfo{}, errors.New("release is unavailable")
	}
	latest := strings.TrimPrefix(strings.TrimSpace(release.Version), "v")
	if latest == "" {
		return UpdateInfo{}, errors.New("latest GitHub release has no version tag")
	}

	releaseURL, _ := release.Metadata["github.release.htmlURL"].(string)
	if err := validateReleaseURL(releaseURL); err != nil {
		return UpdateInfo{}, err
	}

	return UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		Available:      compareVersions(latest, current) > 0,
		ReleaseName:    strings.TrimSpace(release.Name),
		ReleaseURL:     releaseURL,
		PublishedAt:    release.PublishedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Notes:          strings.TrimSpace(release.Notes),
		Message:        fmt.Sprintf("Dockiva %s is available.", latest),
		ArtifactName:   release.Artifact.Filename,
		ArtifactSize:   release.Artifact.Size,
	}, nil
}

func (s *AppService) OpenUpdatePage(releaseURL string) error {
	if err := validateReleaseURL(releaseURL); err != nil {
		return err
	}
	if app := application.Get(); app != nil && app.Browser != nil {
		return app.Browser.OpenURL(releaseURL)
	}
	return exec.Command("/usr/bin/open", releaseURL).Start() // the core, for the native front end
}

func validateReleaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid release URL")
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return errors.New("release URL must use github.com over HTTPS")
	}
	releasePath := strings.ToLower(parsed.EscapedPath())
	if !strings.HasPrefix(releasePath, "/tomnerb/dockiva/releases/") &&
		!strings.HasPrefix(releasePath, "/tomnerb/devstack/releases/") {
		return errors.New("release URL does not belong to Tomnerb/Dockiva")
	}
	return nil
}

func compareVersions(left, right string) int {
	leftParts, leftPrerelease := versionParts(left)
	rightParts, rightPrerelease := versionParts(right)
	for index := 0; index < 3; index++ {
		if leftParts[index] > rightParts[index] {
			return 1
		}
		if leftParts[index] < rightParts[index] {
			return -1
		}
	}
	if leftPrerelease != rightPrerelease {
		if leftPrerelease {
			return -1
		}
		return 1
	}
	return 0
}

func versionParts(version string) ([3]int, bool) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	coreAndPrerelease := strings.SplitN(version, "-", 2)
	version = coreAndPrerelease[0]
	version = strings.SplitN(version, "+", 2)[0]
	parts := strings.Split(version, ".")
	result := [3]int{}
	for index := 0; index < len(parts) && index < len(result); index++ {
		result[index], _ = strconv.Atoi(parts[index])
	}
	return result, len(coreAndPrerelease) > 1
}
