package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type SeedService struct {
	cfg       *config.Config
	users     *repository.UserRepository
	resources *repository.ResourceRepository
	client    *http.Client
}

func NewSeedService(cfg *config.Config, users *repository.UserRepository, resources *repository.ResourceRepository) *SeedService {
	return &SeedService{cfg: cfg, users: users, resources: resources, client: &http.Client{Timeout: 15 * time.Second}}
}

// Run seeds an initial admin account (via the Supabase Admin API, so it
// requires SUPABASE_SERVICE_ROLE_KEY) and a handful of sample resources,
// but only if the respective tables are currently empty. Safe to call on
// every startup.
func (s *SeedService) Run(ctx context.Context) {
	userCount, err := s.users.Count(ctx)
	if err != nil {
		log.Printf("seed: failed to count users: %v", err)
	} else if userCount == 0 {
		if err := s.seedAdmin(ctx); err != nil {
			log.Printf("seed: failed to seed admin user: %v", err)
		}
	}

	resourceCount, err := s.resources.Count(ctx)
	if err != nil {
		log.Printf("seed: failed to count resources: %v", err)
	} else if resourceCount == 0 {
		if err := s.seedResources(ctx); err != nil {
			log.Printf("seed: failed to seed resources: %v", err)
		}
	}
}

func (s *SeedService) seedAdmin(ctx context.Context) error {
	if s.cfg.SupabaseServiceKey == "" {
		log.Println("seed: SUPABASE_SERVICE_ROLE_KEY not set, skipping admin auto-creation")
		return nil
	}

	payload := map[string]interface{}{
		"email":         s.cfg.SeedAdminEmail,
		"password":      s.cfg.SeedAdminPassword,
		"email_confirm": true,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/auth/v1/admin/users", s.cfg.SupabaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseServiceKey)
	req.Header.Set("Authorization", "Bearer "+s.cfg.SupabaseServiceKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("supabase admin API returned %d: %s", resp.StatusCode, string(raw))
	}

	var created struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return fmt.Errorf("supabase admin API did not return a user id: %s", string(raw))
	}

	if _, err := s.users.Upsert(ctx, created.ID, created.Email, s.cfg.SeedAdminFullName, nil, models.RoleAdmin, models.UserStatusApproved); err != nil {
		return err
	}

	log.Printf("seed: created initial admin user %s (change the password after first login)", created.Email)
	return nil
}

func (s *SeedService) seedResources(ctx context.Context) error {
	cap8 := 8
	cap4 := 4
	seats4 := 4
	seats2 := 2
	fuelPetrol := "petrol"
	fuelElectric := "electric"

	samples := []models.ResourceInput{
		{
			Type: models.ResourceTypeRoom, Name: "Ballroom A", Description: "Large conference room with projector",
			Location: "3rd Floor", Capacity: &cap8, Amenities: []string{"projector", "whiteboard", "video conference"},
		},
		{
			Type: models.ResourceTypeRoom, Name: "Meeting Room 2", Description: "Small meeting room",
			Location: "2nd Floor", Capacity: &cap4, Amenities: []string{"whiteboard"},
		},
		{
			Type: models.ResourceTypeCar, Name: "Toyota Avanza", Description: "Company car for field visits",
			Location: "Basement Parking", LicensePlate: strPtr("BK 1234 XY"), Seats: &seats4, FuelType: &fuelPetrol,
		},
		{
			Type: models.ResourceTypeBike, Name: "Electric Scooter 1", Description: "For quick nearby errands",
			Location: "Ground Floor Lobby", LicensePlate: strPtr("BK 5678 ZZ"), Seats: &seats2, FuelType: &fuelElectric,
		},
	}

	for _, in := range samples {
		if _, err := s.resources.Create(ctx, in); err != nil {
			return err
		}
	}

	log.Printf("seed: created %d sample resources", len(samples))
	return nil
}

func strPtr(s string) *string { return &s }
