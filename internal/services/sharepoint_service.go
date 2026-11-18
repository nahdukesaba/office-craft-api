package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"office-craft-api/internal/config"
)

const graphResponseLimit = 64 * 1024

// Plan A (SP_SITE_PATH empty): the signed-in user's OneDrive, Files.ReadWrite.
// Plan B (SP_SITE_PATH set): a SharePoint site library, Files.ReadWrite.All
// (needs admin consent in the Kementerian PKP tenant). Each plan keeps its own
// stored refresh token so switching never reuses a token with the wrong scope.
const (
	oneDriveTokenID   = "onedrive"
	oneDriveScope     = "offline_access Files.ReadWrite"
	sharePointTokenID = "sharepoint"
	sharePointScope   = "offline_access Files.ReadWrite.All"
)

// GraphTokenStore persists the newest Microsoft Graph refresh token.
// Graph rotates the refresh token on every redemption.
type GraphTokenStore interface {
	GetRefreshToken(ctx context.Context, id string) (string, error)
	SaveRefreshToken(ctx context.Context, id, token string) error
}

// SharePointService stores files in the user's OneDrive (Plan A) or one
// SharePoint site's document library (Plan B) through Microsoft Graph, acting as the user who granted the delegated
// refresh token. Its tokens must never be returned to the browser.
type SharePointService struct {
	loginBase  string
	graphBase  string
	tenant     string
	clientID   string
	secret     string
	envRefresh string
	sitePath   string // empty: OneDrive (/me/drive)
	scope      string
	tokenID    string
	folder     string
	tokens     GraphTokenStore
	client     *http.Client

	mu          sync.Mutex
	accessToken string
	accessExp   time.Time
	driveID     string
}

// NewSharePointService returns nil when Graph storage is not fully configured.
func NewSharePointService(cfg *config.Config, tokens GraphTokenStore) *SharePointService {
	if !cfg.SharePointEnabled() {
		return nil
	}
	folder := strings.TrimSpace(cfg.SPFolder)
	if folder == "" {
		folder = "booking-proofs"
	}
	sitePath := strings.Trim(cfg.SPSitePath, "/")
	scope, tokenID := oneDriveScope, oneDriveTokenID
	if sitePath != "" {
		scope, tokenID = sharePointScope, sharePointTokenID
		log.Printf("booking proofs: SharePoint site %s", sitePath)
	} else {
		log.Println("booking proofs: OneDrive of the MS_REFRESH_TOKEN user")
	}
	return &SharePointService{
		loginBase:  "https://login.microsoftonline.com",
		graphBase:  "https://graph.microsoft.com/v1.0",
		tenant:     cfg.MSTenantID,
		clientID:   cfg.MSClientID,
		secret:     cfg.MSClientSecret,
		envRefresh: cfg.MSRefreshToken,
		sitePath:   sitePath,
		scope:      scope,
		tokenID:    tokenID,
		folder:     folder,
		tokens:     tokens,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

type graphTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *SharePointService) redeem(ctx context.Context, refresh string) (*graphTokenResponse, error) {
	form := url.Values{
		"client_id":     {s.clientID},
		"client_secret": {s.secret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"scope":         {s.scope},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		s.loginBase+"/"+url.PathEscape(s.tenant)+"/oauth2/v2.0/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, errors.New("failed to construct graph token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.New("graph token request failed")
	}
	defer resp.Body.Close()

	var out graphTokenResponse
	if err := decodeGraph(resp, &out, "graph token refresh"); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, errors.New("graph token refresh returned no access token")
	}
	return &out, nil
}

// token returns a cached access token, refreshing it under the lock so
// parallel uploads do not redeem the refresh token at the same time.
func (s *SharePointService) token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.accessToken != "" && time.Now().Before(s.accessExp) {
		return s.accessToken, nil
	}

	stored := ""
	if s.tokens != nil {
		t, err := s.tokens.GetRefreshToken(ctx, s.tokenID)
		if err != nil {
			log.Printf("graph refresh token load failed: %v", err)
		}
		stored = strings.TrimSpace(t)
	}

	// Prefer the rotated token from the DB; fall back to MS_REFRESH_TOKEN so
	// re-signing in and updating the env recovers from a dead stored token.
	candidates := make([]string, 0, 2)
	if stored != "" {
		candidates = append(candidates, stored)
	}
	if s.envRefresh != "" && s.envRefresh != stored {
		candidates = append(candidates, s.envRefresh)
	}

	lastErr := errors.New("no Microsoft Graph refresh token available")
	for _, refresh := range candidates {
		out, err := s.redeem(ctx, refresh)
		if err != nil {
			lastErr = err
			continue
		}

		life := time.Duration(out.ExpiresIn) * time.Second
		if life > 10*time.Minute {
			life -= 5 * time.Minute
		} else {
			life /= 2
		}
		s.accessToken = out.AccessToken
		s.accessExp = time.Now().Add(life)

		if out.RefreshToken != "" && out.RefreshToken != stored && s.tokens != nil {
			if err := s.tokens.SaveRefreshToken(ctx, s.tokenID, out.RefreshToken); err != nil {
				log.Printf("graph refresh token save failed: %v", err)
			}
		}
		return s.accessToken, nil
	}

	log.Printf("graph sign-in needed: refresh token rejected or missing (%v); repeat the consent flow and update MS_REFRESH_TOKEN", lastErr)
	return "", lastErr
}

func (s *SharePointService) do(
	ctx context.Context,
	method, target, contentType string,
	body io.Reader,
) (*http.Response, error) {
	tok, err := s.token(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, s.graphBase+target, body)
	if err != nil {
		return nil, errors.New("failed to construct graph request")
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.New("graph request failed")
	}

	if resp.StatusCode == http.StatusUnauthorized {
		s.mu.Lock()
		s.accessToken = ""
		s.mu.Unlock()
	}
	return resp, nil
}

func decodeGraph(resp *http.Response, out any, op string) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, graphResponseLimit))
		return fmt.Errorf("%s failed: HTTP %d", op, resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, graphResponseLimit))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, graphResponseLimit)).Decode(out); err != nil {
		return fmt.Errorf("invalid %s response", op)
	}
	return nil
}

func (s *SharePointService) getJSON(ctx context.Context, target string, out any, op string) error {
	resp, err := s.do(ctx, http.MethodGet, target, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeGraph(resp, out, op)
}

// drive resolves and caches the OneDrive or site default document library id.
func (s *SharePointService) drive(ctx context.Context) (string, error) {
	s.mu.Lock()
	id := s.driveID
	s.mu.Unlock()
	if id != "" {
		return id, nil
	}

	driveTarget := "/me/drive"
	if s.sitePath != "" {
		var site struct {
			ID string `json:"id"`
		}
		if err := s.getJSON(ctx, "/sites/"+s.sitePath, &site, "sharepoint site lookup"); err != nil {
			return "", err
		}
		if site.ID == "" {
			return "", errors.New("sharepoint site lookup returned no id")
		}
		driveTarget = "/sites/" + url.PathEscape(site.ID) + "/drive"
	}

	var drive struct {
		ID string `json:"id"`
	}
	if err := s.getJSON(ctx, driveTarget, &drive, "graph drive lookup"); err != nil {
		return "", err
	}
	if drive.ID == "" {
		return "", errors.New("graph drive lookup returned no id")
	}

	s.mu.Lock()
	s.driveID = drive.ID
	s.mu.Unlock()
	return drive.ID, nil
}

// itemTarget accepts the "<driveID>/<itemID>" ref Upload returns, so proofs
// stay readable after switching plans. A bare item id means the current drive.
func (s *SharePointService) itemTarget(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	driveID, itemID, hasDrive := strings.Cut(ref, "/")
	if !hasDrive {
		driveID, itemID = "", ref
	}
	if hasInvalidPathSegment(itemID) || (hasDrive && hasInvalidPathSegment(driveID)) {
		return "", errors.New("invalid graph item ref")
	}
	if !hasDrive {
		var err error
		if driveID, err = s.drive(ctx); err != nil {
			return "", err
		}
	}
	return "/drives/" + url.PathEscape(driveID) + "/items/" + url.PathEscape(itemID), nil
}

// Upload stores file at <folder>/<path> and returns the item ref
// ("<driveID>/<itemID>") and webUrl.
// Simple PUT upload is enough: proofs are capped at MaxStorageUploadBytes,
// well under Graph's simple-upload limit.
func (s *SharePointService) Upload(
	ctx context.Context,
	path, mime string,
	file []byte,
) (string, string, error) {
	if len(file) == 0 || len(file) > MaxStorageUploadBytes {
		return "", "", errors.New("invalid storage object size")
	}
	mime = strings.TrimSpace(mime)
	if mime == "" {
		return "", "", errors.New("storage content type is required")
	}

	object, err := storagePath(s.folder, path)
	if err != nil {
		return "", "", err
	}
	driveID, err := s.drive(ctx)
	if err != nil {
		return "", "", err
	}

	resp, err := s.do(
		ctx,
		http.MethodPut,
		"/drives/"+url.PathEscape(driveID)+"/root:/"+object+":/content?@microsoft.graph.conflictBehavior=fail",
		mime,
		bytes.NewReader(file),
	)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var item struct {
		ID     string `json:"id"`
		WebURL string `json:"webUrl"`
	}
	if err := decodeGraph(resp, &item, "sharepoint upload"); err != nil {
		return "", "", err
	}
	if item.ID == "" {
		return "", "", errors.New("sharepoint upload returned no item id")
	}
	return driveID + "/" + item.ID, item.WebURL, nil
}

func (s *SharePointService) Delete(ctx context.Context, itemID string) error {
	target, err := s.itemTarget(ctx, itemID)
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, target, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeGraph(resp, nil, "sharepoint delete")
}

// DownloadURL returns Graph's short-lived pre-authenticated download link,
// usable as <img src> without a Microsoft sign-in.
func (s *SharePointService) DownloadURL(ctx context.Context, itemID string) (string, error) {
	target, err := s.itemTarget(ctx, itemID)
	if err != nil {
		return "", err
	}

	var item struct {
		DownloadURL string `json:"@microsoft.graph.downloadUrl"`
	}
	if err := s.getJSON(ctx, target, &item, "sharepoint download URL"); err != nil {
		return "", err
	}

	parsed, err := url.Parse(item.DownloadURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("sharepoint did not return a valid download URL")
	}
	return item.DownloadURL, nil
}
