// Command gmailgen writes the Go types of the Gmail REST API from Google's
// Discovery document.
//
// Gmail has no OpenAPI document, and the Discovery endpoint only serves the
// current revision. The generated header therefore records the revision and a
// hash of the document it came from; the document itself is not stored in the
// repository. Proxy settings come from the environment, so running this behind
// a local proxy only needs HTTPS_PROXY.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// discoveryURL is the only source of the specification.
const discoveryURL = "https://gmail.googleapis.com/$discovery/rest?version=v1"

// maxDocument bounds the download; the document is roughly 200 KiB.
const maxDocument = 8 << 20

func main() {
	out := flag.String("o", "discovery_types.go", "output file, relative to the working directory")
	pkg := flag.String("pkg", "gmail", "package clause of the output file")
	flag.Parse()

	if err := run(*out, *pkg); err != nil {
		fmt.Fprintf(os.Stderr, "gmailgen: %v\n", err)
		os.Exit(1)
	}
}

// run downloads the specification, renders it and replaces the output file.
func run(outPath, pkg string) error {
	raw, err := download(discoveryURL)
	if err != nil {
		return err
	}
	src, err := generate(raw, pkg)
	if err != nil {
		return err
	}
	return writeFile(outPath, src)
}

// download fetches the Discovery document. The client has no Transport of its
// own, so the default transport applies and HTTPS_PROXY is honoured.
func download(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "yauzin-gmailgen")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download discovery document: %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument))
	if err != nil {
		return nil, fmt.Errorf("read discovery document: %w", err)
	}
	return raw, nil
}

// writeFile replaces path atomically: a failed run never leaves a half written
// file behind, and the mode does not depend on the umask.
func writeFile(path string, src []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gmailgen-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	discard := func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(src); err != nil {
		discard()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
