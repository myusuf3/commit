package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		current, latest      string
		force, want, wantErr bool
	}{
		{"1.0.0", "v1.1.0", false, true, false}, {"1.1.0", "v1.1.0", false, false, false},
		{"2.0.0", "1.1.0", false, false, false}, {"2.0.0", "1.1.0", true, true, false},
		{"dev", "1.1.0", false, false, true}, {"dev", "1.1.0", true, true, false},
		{"1.0.0", "bad", true, false, true},
	} {
		got, err := Newer(tc.current, tc.latest, tc.force)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Fatalf("%+v => %v %v", tc, got, err)
		}
	}
}

func TestChecksumMandatory(t *testing.T) {
	data := []byte("binary")
	sum := sha256.Sum256(data)
	manifest := []byte(strings.ToLower(fmtHash(sum)) + "  archive.tar.gz\n")
	if err := VerifyChecksum(data, "archive.tar.gz", manifest); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte("bad  archive.tar.gz"), append(manifest, manifest...)} {
		if VerifyChecksum(data, "archive.tar.gz", bad) == nil {
			t.Fatal("accepted absent, wrong, or ambiguous checksum")
		}
	}
	if VerifyChecksum([]byte("modified"), "archive.tar.gz", manifest) == nil {
		t.Fatal("accepted modified data")
	}
}

func fmtHash(hash [32]byte) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for _, v := range hash {
		b.WriteByte(hex[v>>4])
		b.WriteByte(hex[v&15])
	}
	return b.String()
}

func tarArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: 6, Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("binary")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtractArchives(t *testing.T) {
	data, err := extract(tarArchive(t, "commit"), "archive.tar.gz", "commit")
	if err != nil || string(data) != "binary" {
		t.Fatalf("data=%s err=%v", data, err)
	}
	for _, names := range [][]string{{"../commit"}, {"/commit"}, {"commit", "commit"}, {"other"}} {
		if _, err := extract(tarArchive(t, names...), "archive.tar.gz", "commit"); err == nil {
			t.Fatalf("accepted names %v", names)
		}
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("commit.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("binary"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := extract(b.Bytes(), "archive.zip", "commit.exe"); err != nil || string(data) != "binary" {
		t.Fatalf("zip: %s %v", data, err)
	}
}

func TestInstallPreservesOldBinaryOnValidationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commit")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	err := install(path, []byte("new"), func(string) error { return errors.New("invalid executable") })
	if err == nil {
		t.Fatal("ignored verification failure")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old" {
		t.Fatal("destroyed original")
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("left temporary files or lock")
	}
	if runtime.GOOS != "windows" {
		if err := install(path, []byte("new"), func(string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(path)
		if string(data) != "new" {
			t.Fatal("did not install")
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckUsesConfiguredRepositoryAndRequiresChecksum(t *testing.T) {
	for _, checksum := range []bool{true, false} {
		c := &Client{Repository: "person/commit", Current: "1.0.0", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.github.com/repos/person/commit/releases/latest" {
				t.Errorf("unexpected URL %s", r.URL)
			}
			release := Release{Tag: "v1.2.0", Assets: []Asset{{Name: ArchiveName(), URL: "https://github.com/person/commit/releases/download/v1.2.0/" + ArchiveName()}}}
			if checksum {
				release.Assets = append(release.Assets, Asset{Name: "checksums.txt", URL: "https://github.com/person/commit/releases/download/v1.2.0/checksums.txt"})
			}
			b, _ := json.Marshal(release)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
		})}}
		p, err := c.Check(context.Background(), false)
		if checksum && (err != nil || p.Version != "v1.2.0") {
			t.Fatalf("plan=%v err=%v", p, err)
		}
		if !checksum && err == nil {
			t.Fatal("checksum was optional")
		}
	}
	if _, err := (&Client{}).Check(context.Background(), false); err == nil {
		t.Fatal("implicit release destination")
	}
}
