// Command pricegen regenerates internal/pricing/data/*.json from the providers' own price APIs.
//
//	go run ./tools/pricegen aws       # AWS Price List bulk files (public, ~430 MB per region, streamed)
//	go run ./tools/pricegen azure     # Azure Retail Prices API (public)
//	go run ./tools/pricegen fx        # ECB euro reference rates (public)
//	go run ./tools/pricegen upcloud   # UpCloud API, needs UPCLOUD_TOKEN (see upcloud.go)
//
// Run from the repo root. CI runs it monthly and opens a pull request with the changes.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const outDir = "internal/pricing/data"

var client = &http.Client{Timeout: 30 * time.Minute}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pricegen aws|azure|fx|upcloud [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "aws":
		err = genAWS()
	case "azure":
		err = genAzure()
	case "fx":
		err = genFX()
	case "upcloud":
		err = genUpCloud(os.Args[2:])
	default:
		err = fmt.Errorf("unknown source %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pricegen:", err)
		os.Exit(1)
	}
}

func get(url string, header http.Header) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s: %s", url, resp.Status, body)
	}
	return resp.Body, nil
}

func writeJSON(name string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func today() string { return time.Now().UTC().Format("2006-01-02") }
