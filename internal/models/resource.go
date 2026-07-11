package models

import "time"

const (
	ResourceTypeRoom = "room"
	ResourceTypeCar  = "car"
	ResourceTypeBike = "bike"
)

// DefaultResourceColor is used when a resource is created without an
// explicit color (matches the DB column's default).
const DefaultResourceColor = "#64748B"

// Resource is a single-table-inheritance representation of Room / Car / Bike.
// Fields not relevant to a given type are simply nil / omitted in JSON.
type Resource struct {
	ID          string  `json:"id" db:"id"`
	Type        string  `json:"type" db:"type"`
	Name        string  `json:"name" db:"name"`
	Description string  `json:"description" db:"description"`
	Location    string  `json:"location" db:"location"`
	PhotoURL    *string `json:"photoUrl" db:"photo_url"`
	IsAvailable bool    `json:"isAvailable" db:"is_available"`
	// Color is a hex string (e.g. "#0EA5E9") used for calendar/badge
	// rendering on the frontend - every resource type has one.
	Color string `json:"color" db:"color"`

	// Room-specific
	Capacity  *int     `json:"capacity,omitempty" db:"capacity"`
	Amenities []string `json:"amenities,omitempty" db:"amenities"`

	// Car / Bike-specific
	LicensePlate *string `json:"licensePlate,omitempty" db:"license_plate"`
	FuelType     *string `json:"fuelType,omitempty" db:"fuel_type"`

	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

// ResourceInput is the shape accepted on create/update.
type ResourceInput struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Location     string   `json:"location"`
	PhotoURL     *string  `json:"photoUrl"`
	IsAvailable  *bool    `json:"isAvailable"`
	Color        string   `json:"color"`
	Capacity     *int     `json:"capacity"`
	Amenities    []string `json:"amenities"`
	LicensePlate *string  `json:"licensePlate"`
	FuelType     *string  `json:"fuelType"`
}
