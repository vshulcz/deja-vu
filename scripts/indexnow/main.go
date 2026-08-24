// Command indexnow tells the IndexNow endpoint which pages changed, so Bing,
// Yandex and the assistants built on them re-crawl in hours instead of whenever
// they get round to it. Google does not participate; nothing here reaches it.
//
// The key is a file served from the docs root, and the submitted URLs must sit
// under that same path — IndexNow verifies ownership by fetching it.
//
//	go run ./scripts/indexnow            # every URL in docs/sitemap.xml
//	go run ./scripts/indexnow -dry-run   # print what would be sent
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	host        = "vshulcz.github.io"
	keyFile     = "3476c6640a9a6cf845450eaf474f12ee"
	keyLocation = "https://vshulcz.github.io/deja-vu/" + keyFile + ".txt"
	endpoint    = "https://api.indexnow.org/IndexNow"
	sitemapPath = "docs/sitemap.xml"
)

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

type submission struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "print the payload instead of sending it")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if err := run(*root, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "indexnow: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, dryRun bool) error {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sitemapPath)))
	if err != nil {
		return err
	}
	var set urlset
	if err := xml.Unmarshal(b, &set); err != nil {
		return fmt.Errorf("sitemap: %w", err)
	}

	var urls []string
	for _, u := range set.URLs {
		loc := strings.TrimSpace(u.Loc)
		// A URL outside the key's directory is rejected for the whole batch,
		// so it is dropped here with a line saying so rather than silently.
		if !strings.HasPrefix(loc, "https://"+host+"/deja-vu/") {
			fmt.Fprintf(os.Stderr, "skipping %s: outside the key's path\n", loc)
			continue
		}
		urls = append(urls, loc)
	}
	if len(urls) == 0 {
		return fmt.Errorf("no submittable URLs in %s", sitemapPath)
	}

	payload, err := json.Marshal(submission{
		Host:        host,
		Key:         keyFile,
		KeyLocation: keyLocation,
		URLList:     urls,
	})
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Printf("%d URLs\n%s\n", len(urls), payload)
		return nil
	}

	// The key file has to be live before submitting: IndexNow fetches it to
	// verify ownership, and a 404 there fails the whole batch.
	client := &http.Client{Timeout: 20 * time.Second}
	if resp, err := client.Get(keyLocation); err != nil {
		return fmt.Errorf("key file: %w", err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("key file at %s returns %d — publish it before submitting", keyLocation, resp.StatusCode)
		}
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// 200 accepted, 202 accepted but the key is still being validated.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("endpoint returned %s", resp.Status)
	}
	fmt.Printf("submitted %d URLs, %s\n", len(urls), resp.Status)
	return nil
}
