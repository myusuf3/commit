// Package update installs explicitly configured GitHub releases. There is no
// baked-in repository, background network check, or checksum-optional path.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	version "github.com/hashicorp/go-version"
	"github.com/myusuf3/commit/internal/github"
	"github.com/myusuf3/commit/internal/httpapi"
)

const maxArchiveBytes = 128 << 20
const maxBinaryBytes = 64 << 20

type Client struct {
	HTTP       *http.Client
	Repository string
	Token      string
	Current    string
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Plan struct{ Version, ArchiveURL, ChecksumURL, ArchiveName string }

func Newer(current, latest string, force bool) (bool, error) {
	lv, err := version.NewSemver(latest)
	if err != nil {
		return false, errors.New("release has an invalid semantic version")
	}
	if current == "dev" {
		if force {
			return true, nil
		}
		return false, errors.New("development build; use --force to replace it with a published release")
	}
	cv, err := version.NewSemver(current)
	if err != nil {
		return false, errors.New("current build has an invalid semantic version")
	}
	return force || lv.GreaterThan(cv), nil
}

func ArchiveName() string {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return "commit_" + runtime.GOOS + "_" + runtime.GOARCH + ext
}

func (c *Client) Check(ctx context.Context, force bool) (*Plan, error) {
	if c.Repository == "" {
		return nil, errors.New("no release repository configured; set release_repository = \"owner/repo\" or COMMIT_RELEASE_REPOSITORY after publishing this app")
	}
	if err := github.ValidateRepository(c.Repository); err != nil {
		return nil, err
	}
	var release Release
	if err := httpapi.JSON(ctx, c.HTTP, "GET", "https://api.github.com/repos/"+c.Repository+"/releases/latest", c.Token, nil, &release); err != nil {
		return nil, err
	}
	if release.Draft || release.Prerelease {
		return nil, errors.New("refusing an unpublished or prerelease update")
	}
	newer, err := Newer(c.Current, release.Tag, force)
	if err != nil || !newer {
		return nil, err
	}
	p := &Plan{Version: release.Tag, ArchiveName: ArchiveName()}
	for _, a := range release.Assets {
		if a.Name == p.ArchiveName {
			p.ArchiveURL = a.URL
		}
		if a.Name == "checksums.txt" {
			p.ChecksumURL = a.URL
		}
	}
	if p.ArchiveURL == "" || p.ChecksumURL == "" {
		return nil, errors.New("release must contain a platform archive and checksums.txt")
	}
	for _, raw := range []string{p.ArchiveURL, p.ChecksumURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || !strings.HasPrefix(u.Path, "/"+c.Repository+"/releases/download/") {
			return nil, errors.New("release asset is not hosted by the configured GitHub repository")
		}
	}
	return p, nil
}

func (c *Client) download(ctx context.Context, raw string, limit int64) ([]byte, error) {
	client := *c.HTTP
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many download redirects")
		}
		host := req.URL.Hostname()
		if req.URL.Scheme != "https" || req.URL.User != nil || (host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
			return errors.New("untrusted release download redirect")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, errors.New("invalid release URL")
	}
	// Public asset requests never carry the API token, including on redirects.
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("release download failed; check network and timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release asset exceeds size limit")
	}
	return data, nil
}

func VerifyChecksum(data []byte, name string, manifest []byte) error {
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	matches := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == name {
			matches++
			if !strings.EqualFold(parts[0], actual) {
				return errors.New("release checksum mismatch")
			}
		}
	}
	if matches != 1 {
		return errors.New("release checksum is missing or ambiguous")
	}
	return nil
}

func extract(data []byte, archiveName, binaryName string) ([]byte, error) {
	var found []byte
	read := func(r io.Reader) error {
		if found != nil {
			return errors.New("multiple binaries in release archive")
		}
		b, err := io.ReadAll(io.LimitReader(r, maxBinaryBytes+1))
		if err != nil {
			return err
		}
		if len(b) == 0 || len(b) > maxBinaryBytes {
			return errors.New("invalid release binary size")
		}
		found = b
		return nil
	}
	if strings.HasSuffix(archiveName, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, file := range zr.File {
			if file.Name != binaryName || !file.Mode().IsRegular() {
				continue
			}
			r, err := file.Open()
			if err != nil {
				return nil, err
			}
			err = read(r)
			_ = r.Close()
			if err != nil {
				return nil, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, maxArchiveBytes+1))
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Name == binaryName && header.Typeflag == tar.TypeReg {
				if err := read(tr); err != nil {
					return nil, err
				}
			}
		}
	}
	if found == nil {
		return nil, errors.New("release archive does not contain the expected binary")
	}
	return found, nil
}

func (c *Client) Install(ctx context.Context, p Plan) error {
	if runtime.GOOS == "windows" {
		return errors.New("Windows cannot replace a running executable; download and extract the release manually")
	}
	archive, err := c.download(ctx, p.ArchiveURL, maxArchiveBytes)
	if err != nil {
		return err
	}
	manifest, err := c.download(ctx, p.ChecksumURL, 1<<20)
	if err != nil {
		return err
	}
	if err := VerifyChecksum(archive, p.ArchiveName, manifest); err != nil {
		return err
	}
	binary, err := extract(archive, p.ArchiveName, "commit")
	if err != nil {
		return err
	}
	path, err := os.Executable()
	if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	return install(path, binary, func(path string) error {
		verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(verifyCtx, path, "version")
		var out versionOutput
		cmd.Stdout = &out
		cmd.WaitDelay = time.Second
		if err := cmd.Run(); err != nil {
			return errors.New("new binary could not execute 'version'")
		}
		return verifyVersion(out.buffer.String(), p.Version)
	})
}

type versionOutput struct{ buffer bytes.Buffer }

func (w *versionOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 4096 {
		return 0, errors.New("version output exceeds size limit")
	}
	return w.buffer.Write(p)
}

func verifyVersion(output, tag string) error {
	line, _, _ := strings.Cut(output, "\n")
	if !strings.HasPrefix(line, "commit version ") {
		return errors.New("new binary returned invalid version output")
	}
	actual, err := version.NewSemver(strings.TrimPrefix(line, "commit version "))
	expected, expectedErr := version.NewSemver(tag)
	if err != nil || expectedErr != nil || !actual.Equal(expected) {
		return errors.New("new binary version does not match the release")
	}
	return nil
}

// install validates a same-directory temporary executable before atomically
// replacing the old one. Any failure before rename leaves the original intact.
func install(path string, data []byte, verify func(string) error) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("installation target is not a regular file")
	}
	lock, err := os.OpenFile(path+".update.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot lock executable for update; check permissions or a concurrent update")
	}
	_ = lock.Close()
	defer os.Remove(path + ".update.lock")
	f, err := os.CreateTemp(filepath.Dir(path), ".commit-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(0755); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := verify(f.Name()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("replace executable (original unchanged): %w", err)
	}
	return nil
}
