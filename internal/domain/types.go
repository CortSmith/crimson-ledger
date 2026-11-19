package domain

// Reaction is how a contact relates to the player: "friendly", "hostile" or "neutral".
type Reaction string

// Reaction values.
const (
	ReactionFriendly Reaction = "friendly"
	ReactionHostile  Reaction = "hostile"
	ReactionNeutral  Reaction = "neutral"
)

// Valid reports whether r is a known reaction.
func (r Reaction) Valid() bool {
	return r == ReactionFriendly || r == ReactionHostile || r == ReactionNeutral
}

// Ruleset is the realm ruleset: "normal", "pvp", "rp" or "rppvp".
type Ruleset string

// Ruleset values.
const (
	RulesetNormal Ruleset = "normal"
	RulesetPvP    Ruleset = "pvp"
	RulesetRP     Ruleset = "rp"
	RulesetRPPvP  Ruleset = "rppvp"
)

// Valid reports whether r is a known ruleset.
func (r Ruleset) Valid() bool {
	return r == RulesetNormal || r == RulesetPvP || r == RulesetRP || r == RulesetRPPvP
}

// FixKind says how a position was obtained: "exact" or "approx".
type FixKind string

// FixKind values.
const (
	FixExact  FixKind = "exact"
	FixApprox FixKind = "approx"
)

// Valid reports whether k is a known fix kind.
func (k FixKind) Valid() bool { return k == FixExact || k == FixApprox }

// Fix is one position of a contact. T is epoch milliseconds, X and Y are 0-100.
type Fix struct {
	T     int64   `json:"t"`
	MapID int     `json:"mapId"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Kind  FixKind `json:"kind"`
}

// Contact is a tracked player with its recent fixes.
type Contact struct {
	GUID     string   `json:"guid"`
	Name     string   `json:"name"`
	Raw      string   `json:"raw"`
	Reaction Reaction `json:"reaction"`
	LastSeen int64    `json:"lastSeen"`
	Fixes    []Fix    `json:"fixes"`
}

// Chart is a map and the rectangle its coordinates cover.
type Chart struct {
	MapID int     `json:"mapId"`
	Zone  string  `json:"zone"`
	X0    float64 `json:"x0"`
	Y0    float64 `json:"y0"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
}

// Kill is one kill event between two GUIDs.
type Kill struct {
	T      int64  `json:"t"`
	Killer string `json:"killer"`
	Victim string `json:"victim"`
	MapID  int    `json:"mapId"`
}

// Ping is one uploaded batch (POST /v1/pings body).
type Ping struct {
	BatchID  string    `json:"batchId"`
	Source   string    `json:"source"`
	Ruleset  Ruleset   `json:"ruleset"`
	SentAt   int64     `json:"sentAt"`
	Contacts []Contact `json:"contacts"`
	Charts   []Chart   `json:"charts"`
	Kills    []Kill    `json:"kills"`
}

// SweepContact is one contact in a GET /v1/sweep response: its latest fix flattened.
type SweepContact struct {
	GUID     string   `json:"guid"`
	Name     string   `json:"name"`
	Reaction Reaction `json:"reaction"`
	LastSeen int64    `json:"lastSeen"`
	MapID    int      `json:"mapId"`
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	Kind     FixKind  `json:"kind"`
}

// KillTotal is one row of a GET /v1/bounty ranking.
type KillTotal struct {
	GUID     string `json:"guid"`
	Name     string `json:"name"`
	Kills    int    `json:"kills"`
	LastKill int64  `json:"lastKill"`
}
