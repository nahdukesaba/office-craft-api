package services

import (
	"context"
	"log"

	"office-craft-api/internal/models"
)

type SeedService struct {
	resources resourceSeedStore
}

type resourceSeedStore interface {
	Count(ctx context.Context) (int64, error)
	Create(
		ctx context.Context,
		input models.ResourceInput,
	) (*models.Resource, error)
}

func NewSeedService(resources resourceSeedStore) *SeedService {
	return &SeedService{resources: resources}
}

// Run seeds business resources only. Central SSO is the exclusive runtime
// owner for creating Supabase Auth identities and Central SSO profiles.
func (s *SeedService) Run(ctx context.Context) {
	resourceCount, err := s.resources.Count(ctx)
	if err != nil {
		log.Printf("seed: failed to count resources: %v", err)
	} else if resourceCount == 0 {
		if err := s.seedResources(ctx); err != nil {
			log.Printf("seed: failed to seed resources: %v", err)
		}
	}
}

func (s *SeedService) seedResources(ctx context.Context) error {
	cap8 := 8
	cap4 := 4
	fuelPetrol := "petrol"
	fuelElectric := "electric"

	samples := []models.ResourceInput{
		{
			Type: models.ResourceTypeRoom, Name: "Ballroom A", Description: "Large conference room with projector",
			Location: "3rd Floor", Color: "#0EA5E9", Capacity: &cap8, Amenities: []string{"projector", "whiteboard", "video conference"},
		},
		{
			Type: models.ResourceTypeRoom, Name: "Meeting Room 2", Description: "Small meeting room",
			Location: "2nd Floor", Color: "#6366F1", Capacity: &cap4, Amenities: []string{"whiteboard"},
		},
		{
			Type: models.ResourceTypeCar, Name: "Toyota Avanza", Description: "Company car for field visits",
			Location: "Basement Parking", Color: "#F59E0B", LicensePlate: strPtr("BK 1234 XY"), FuelType: &fuelPetrol,
		},
		{
			Type: models.ResourceTypeBike, Name: "Electric Scooter 1", Description: "For quick nearby errands",
			Location: "Ground Floor Lobby", Color: "#10B981", LicensePlate: strPtr("BK 5678 ZZ"), FuelType: &fuelElectric,
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
