package utils

import (
	"log"
	"sync"
	"time"
)

const jakartaTZ = "Asia/Jakarta"

var (
	jakartaLoc     *time.Location
	jakartaLocOnce sync.Once
)

// JakartaLocation returns the Asia/Jakarta (WIB, UTC+7) time.Location.
// Falls back to a fixed UTC+7 zone if the IANA tzdata isn't available on
// the host (this can happen on stripped-down Windows deployments), so
// callers never need to handle a load error.
func JakartaLocation() *time.Location {
	jakartaLocOnce.Do(func() {
		loc, err := time.LoadLocation(jakartaTZ)
		if err != nil {
			log.Printf("utils: could not load %s tzdata (%v), falling back to fixed UTC+7", jakartaTZ, err)
			loc = time.FixedZone("WIB", 7*60*60)
		}
		jakartaLoc = loc
	})
	return jakartaLoc
}

// TodayJakarta returns today's calendar date (midnight) in Asia/Jakarta.
func TodayJakarta() time.Time {
	now := time.Now().In(JakartaLocation())
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, JakartaLocation())
}

// DateOnlyJakarta truncates a timestamp down to its Asia/Jakarta calendar date.
func DateOnlyJakarta(t time.Time) time.Time {
	local := t.In(JakartaLocation())
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, JakartaLocation())
}

// WithinInclusiveDateRange reports whether `day` falls within [from, to],
// comparing calendar dates only (time-of-day is ignored).
func WithinInclusiveDateRange(day, from, to time.Time) bool {
	d := DateOnlyJakarta(day)
	f := DateOnlyJakarta(from)
	t := DateOnlyJakarta(to)
	return !d.Before(f) && !d.After(t)
}
