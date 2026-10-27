// Package release builds the iOS IPA and uploads it to TestFlight using the app's Makefile.
package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/LidoxLee/chasage-bot/internal/gitx"
	"github.com/LidoxLee/chasage-bot/internal/shell"
)

// Releaser owns a dedicated app clone used only for release builds.
type Releaser struct {
	R           shell.Runner
	Git         gitx.Git
	Slug        string
	Dir         string // dedicated clone, never used for task worktrees
	Base        string
	ASCKeyID    string
	ASCIssuerID string
}

// Build resets the clone to origin/base and produces an IPA with the given build number.
func (r Releaser) Build(ctx context.Context, log io.Writer, buildNumber int) (string, error) {
	if err := r.Git.EnsureClone(ctx, log, r.Slug, r.Dir); err != nil {
		return "", err
	}
	if err := r.Git.ResetToBase(ctx, log, r.Dir, r.Base); err != nil {
		return "", err
	}
	ipaDir := filepath.Join(r.Dir, "build", "ios", "ipa")
	// build/ is git-ignored, so a stale IPA would survive the reset and could be uploaded by mistake.
	if err := os.RemoveAll(ipaDir); err != nil {
		return "", err
	}
	steps := []shell.Cmd{
		{Name: "fvm", Args: []string{"install"}},
		{Name: "fvm", Args: []string{"flutter", "pub", "get"}},
		{Name: "fvm", Args: []string{"dart", "run", "build_runner", "build", "--delete-conflicting-outputs"}},
		{Name: "make", Args: []string{"build-ipa", "BUILD_NUMBER=" + strconv.Itoa(buildNumber)}, Env: r.ascEnv()},
	}
	for _, c := range steps {
		c.Dir = r.Dir
		if _, err := r.R.Run(ctx, log, c); err != nil {
			return "", err
		}
	}
	return newestIPA(ipaDir)
}

// Upload sends the IPA to App Store Connect.
func (r Releaser) Upload(ctx context.Context, log io.Writer, ipa string) error {
	_, err := r.R.Run(ctx, log, shell.Cmd{
		Dir:  r.Dir,
		Name: "make",
		Args: []string{"upload-testflight", "IPA=" + ipa},
		Env:  r.ascEnv(),
	})
	return err
}

// ascEnv lets build-ipa sign and upload-testflight upload with the App Store
// Connect API key instead of an Xcode Apple ID login.
func (r Releaser) ascEnv() []string {
	return []string{"ASC_KEY_ID=" + r.ASCKeyID, "ASC_ISSUER_ID=" + r.ASCIssuerID}
}

func newestIPA(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.ipa"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", errors.New("build/ios/ipa 底下找不到 IPA")
	}
	type f struct {
		path string
		mod  int64
	}
	files := make([]f, 0, len(matches))
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", m, err)
		}
		files = append(files, f{m, st.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	return files[0].path, nil
}
