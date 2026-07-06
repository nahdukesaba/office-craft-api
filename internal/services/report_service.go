package services

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"time"

	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/utils"
)

type ReportService struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
	users     *repository.UserRepository
}

func NewReportService(bookings *repository.BookingRepository, resources *repository.ResourceRepository, users *repository.UserRepository) *ReportService {
	return &ReportService{bookings: bookings, resources: resources, users: users}
}

// enrichedRow is the flattened, report-friendly shape of one booking, with
// resource/user names already resolved (avoiding repeat lookups via the
// caches below when a resource or user shows up in many bookings).
type enrichedRow struct {
	booking      models.Booking
	resourceName string
	resourceType string
	userFullName string
	userEmail    string
}

func (s *ReportService) loadEnrichedRows(ctx context.Context, from, to time.Time, statusFilter string) ([]enrichedRow, error) {
	bookings, err := s.bookings.ListForDateRange(ctx, from, to)
	if err != nil {
		return nil, err
	}

	resourceCache := map[string]*models.Resource{}
	userCache := map[string]*models.AppUser{}

	rows := make([]enrichedRow, 0, len(bookings))
	for _, b := range bookings {
		if statusFilter != "" && b.Status != statusFilter {
			continue
		}

		res, ok := resourceCache[b.ResourceID]
		if !ok {
			res, _ = s.resources.GetByID(ctx, b.ResourceID)
			resourceCache[b.ResourceID] = res
		}
		usr, ok := userCache[b.UserID]
		if !ok {
			usr, _ = s.users.GetByID(ctx, b.UserID)
			userCache[b.UserID] = usr
		}

		row := enrichedRow{booking: b, resourceName: "(deleted resource)", userFullName: "(deleted user)"}
		if res != nil {
			row.resourceName = res.Name
			row.resourceType = res.Type
		}
		if usr != nil {
			row.userFullName = usr.FullName
			row.userEmail = usr.Email
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ExportBookingsCSV builds a CSV report of every booking (optionally
// filtered to one status) starting within [from, to).
func (s *ReportService) ExportBookingsCSV(ctx context.Context, from, to time.Time, statusFilter string) ([]byte, error) {
	rows, err := s.loadEnrichedRows(ctx, from, to, statusFilter)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	header := []string{"id", "resourceName", "resourceType", "userFullName", "userEmail", "status", "startTime", "endTime", "purpose", "adminNotes", "createdAt"}
	if err := w.Write(header); err != nil {
		return nil, err
	}

	loc := utils.JakartaLocation()
	for _, r := range rows {
		b := r.booking
		record := []string{
			b.ID,
			r.resourceName,
			r.resourceType,
			r.userFullName,
			r.userEmail,
			b.Status,
			b.StartTime.In(loc).Format(time.RFC3339),
			b.EndTime.In(loc).Format(time.RFC3339),
			b.Purpose,
			b.AdminNotes,
			b.CreatedAt.In(loc).Format(time.RFC3339),
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BookingInsights is simple aggregate data over a date range - counts and
// breakdowns only, deliberately no forecasting or trend modeling.
type BookingInsights struct {
	From                   string          `json:"from"`
	To                     string          `json:"to"`
	TotalBookings          int             `json:"totalBookings"`
	ByStatus               map[string]int  `json:"byStatus"`
	ByResourceType         map[string]int  `json:"byResourceType"`
	ByResource             []ResourceCount `json:"byResource"`
	ByDay                  []DayCount      `json:"byDay"`
	AverageDurationMinutes float64         `json:"averageDurationMinutes"`
	TopUsers               []UserCount     `json:"topUsers"`
	AutoRejectedCount      int             `json:"autoRejectedCount"`
}

type ResourceCount struct {
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	Count        int    `json:"count"`
}

type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type UserCount struct {
	UserID   string `json:"userId"`
	FullName string `json:"fullName"`
	Count    int    `json:"count"`
}

// BuildInsights computes the aggregates described above for every booking
// (all statuses) starting within [from, to).
func (s *ReportService) BuildInsights(ctx context.Context, from, to time.Time) (*BookingInsights, error) {
	rows, err := s.loadEnrichedRows(ctx, from, to, "")
	if err != nil {
		return nil, err
	}

	loc := utils.JakartaLocation()

	insights := &BookingInsights{
		From:           from.In(loc).Format("2006-01-02"),
		To:             to.Add(-24 * time.Hour).In(loc).Format("2006-01-02"), // to is exclusive upper bound internally; report the inclusive last day
		TotalBookings:  len(rows),
		ByStatus:       map[string]int{},
		ByResourceType: map[string]int{},
	}

	resourceCounts := map[string]*ResourceCount{}
	userCounts := map[string]*UserCount{}
	dayCounts := map[string]int{}
	var totalDurationMinutes float64

	for _, r := range rows {
		b := r.booking

		insights.ByStatus[b.Status]++
		if r.resourceType != "" {
			insights.ByResourceType[r.resourceType]++
		}

		if rc, ok := resourceCounts[b.ResourceID]; ok {
			rc.Count++
		} else {
			resourceCounts[b.ResourceID] = &ResourceCount{ResourceID: b.ResourceID, ResourceName: r.resourceName, Count: 1}
		}

		if uc, ok := userCounts[b.UserID]; ok {
			uc.Count++
		} else {
			userCounts[b.UserID] = &UserCount{UserID: b.UserID, FullName: r.userFullName, Count: 1}
		}

		day := b.StartTime.In(loc).Format("2006-01-02")
		dayCounts[day]++

		totalDurationMinutes += b.EndTime.Sub(b.StartTime).Minutes()

		if containsAutoRejectedNote(b.AdminNotes) {
			insights.AutoRejectedCount++
		}
	}

	if len(rows) > 0 {
		insights.AverageDurationMinutes = roundTo2DP(totalDurationMinutes / float64(len(rows)))
	}

	for _, rc := range resourceCounts {
		insights.ByResource = append(insights.ByResource, *rc)
	}
	sort.Slice(insights.ByResource, func(i, j int) bool { return insights.ByResource[i].Count > insights.ByResource[j].Count })

	var allUsers []UserCount
	for _, uc := range userCounts {
		allUsers = append(allUsers, *uc)
	}
	sort.Slice(allUsers, func(i, j int) bool { return allUsers[i].Count > allUsers[j].Count })
	if len(allUsers) > 5 {
		allUsers = allUsers[:5]
	}
	insights.TopUsers = allUsers

	// Fill in every calendar day in the range, including zero-count days,
	// so a frontend chart doesn't have to backfill gaps itself.
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.In(loc).Format("2006-01-02")
		insights.ByDay = append(insights.ByDay, DayCount{Date: key, Count: dayCounts[key]})
	}

	return insights, nil
}

func containsAutoRejectedNote(notes string) bool {
	return len(notes) >= len("Auto-rejected") && notes[:len("Auto-rejected")] == "Auto-rejected"
}

func roundTo2DP(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
