package models

// BookingRules are the admin-controlled borrowing switches.
type BookingRules struct {
	BlockResourceOpenUsage bool `json:"blockResourceOpenUsage"`
	BlockUserUnfinished    bool `json:"blockUserUnfinished"`
}

// BookingRulesInput uses pointers so a missing field is rejected, not read as false.
type BookingRulesInput struct {
	BlockResourceOpenUsage *bool `json:"blockResourceOpenUsage"`
	BlockUserUnfinished    *bool `json:"blockUserUnfinished"`
}

// BookingRuleDetail is the error payload the frontend turns into a warning.
type BookingRuleDetail struct {
	IsSelf        bool     `json:"isSelf"`
	UserFullName  string   `json:"userFullName"`
	ResourceNames []string `json:"resourceNames,omitempty"`
}

// OverdueSummary counts who an overdue reminder reaches.
type OverdueSummary struct {
	Bookings     int `json:"bookings"`
	Users        int `json:"users"`
	WithoutPhone int `json:"withoutPhone"`
}
