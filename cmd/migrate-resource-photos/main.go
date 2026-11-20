// Command migrate-resource-photos copies resource photos stored in Supabase
// Storage to OneDrive/SharePoint (the same Graph backend the API uses) and
// points resources.photo_url at the API photo redirect.
//
// Dry run (default) only lists what would change; -apply writes. The Supabase
// files are left in place as a backup.
//
//	go run ./cmd/migrate-resource-photos -base https://silapet.bp3kpsumatera2.com [-apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/db"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

func main() {
	base := flag.String("base", "", "public site URL, e.g. https://silapet.bp3kpsumatera2.com")
	apply := flag.Bool("apply", false, "upload photos and update resources (default: dry run)")
	flag.Parse()
	if !strings.HasPrefix(*base, "https://") && !strings.HasPrefix(*base, "http://localhost") {
		log.Fatal("-base must be the public https site URL")
	}
	publicBase := strings.TrimRight(*base, "/")

	cfg := config.Load()
	pool, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("database connection error: %v", err)
	}
	defer pool.Close()

	sp := services.NewSharePointService(cfg, repository.NewGraphTokenRepository(pool))
	if sp == nil {
		log.Fatal("MS_* settings missing: Graph storage is not configured")
	}

	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT id, name, photo_url FROM public.resources
		WHERE photo_url LIKE '%/storage/v1/object/%'
		ORDER BY name`)
	if err != nil {
		log.Fatalf("list resources: %v", err)
	}
	type photo struct{ id, name, url string }
	var todo []photo
	for rows.Next() {
		var p photo
		if err := rows.Scan(&p.id, &p.name, &p.url); err != nil {
			log.Fatalf("scan resource: %v", err)
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("list resources: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	failed := 0
	for _, p := range todo {
		// Old names have spaces ("Mobil Xpander.jpeg"); the resource id is URL-safe and unique.
		parsed, err := url.Parse(p.url)
		if err != nil {
			failed++
			log.Printf("FAIL %s: bad photo_url: %v", p.name, err)
			continue
		}
		name := p.id + strings.ToLower(path.Ext(parsed.Path))
		if !*apply {
			fmt.Printf("would move %-30s %s\n", p.name, name)
			continue
		}

		data, mime, err := fetchImage(ctx, client, p.url)
		if err != nil {
			failed++
			log.Printf("FAIL %s: download: %v", p.name, err)
			continue
		}
		driveID, err := sp.UploadPhoto(ctx, name, mime, data)
		if err != nil {
			failed++
			log.Printf("FAIL %s: upload: %v", p.name, err)
			continue
		}
		newURL := publicBase + "/api/resources/photos/" + driveID + "/" + name
		// Only replace the URL we read, so an admin edit in between wins.
		tag, err := pool.Exec(ctx,
			`UPDATE public.resources SET photo_url = $1, updated_at = now() WHERE id = $2 AND photo_url = $3`,
			newURL, p.id, p.url)
		if err != nil || tag.RowsAffected() != 1 {
			failed++
			log.Printf("FAIL %s: update photo_url (err=%v, rows=%d); file is in OneDrive as %s", p.name, err, tag.RowsAffected(), name)
			continue
		}
		fmt.Printf("moved %-30s %s\n", p.name, newURL)
	}

	fmt.Printf("%d photos, %d failed, apply=%v\n", len(todo), failed, *apply)
	if failed > 0 {
		log.Fatal("some photos were not migrated; rerun after fixing (moved rows are skipped)")
	}
}

func fetchImage(ctx context.Context, client *http.Client, photoURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	mime := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", fmt.Errorf("not an image: %q", mime)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(services.MaxStorageUploadBytes)+1))
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}
