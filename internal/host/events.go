package host

import (
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
)

//
//
type Event struct {
	ID         string
	Time       time.Time
	FinishedAt time.Time
	Failed     bool
	Category   string    // DISPATCH / TOOL / DECISION / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string
	Summary    string
	Detail     string
	Kind       string
	Level      string        // info / warn / error / success
	Depth      int
	Duration   time.Duration
	RetryAt    time.Time
}

func (e Event) Running() bool {
	return e.ID != "" && e.FinishedAt.IsZero()
}

type UISnapshot struct {
	Provider             string
	NovelName            string
	ModelName            string
	ModelContextWindow   int
	ThinkingLevel        string
	Style                string
	RuntimeState         string // idle / running / pausing / paused / completed
	StatusLabel          string
	Phase                string
	Flow                 string
	CurrentChapter       int
	TotalChapters        int
	CompletedCount       int
	TotalWordCount       int
	InProgressChapter    int
	PendingRewrites      []int
	RewriteReason        string
	PendingSteer         string
	AdvanceMode          string
	AdvancePermitChapter int
	HasAdvanceHold       bool
	AdvanceHoldReason    string
	RecoveryLabel        string
	IsRunning            bool
	Agents               []AgentSnapshot

	ContextTokens         int
	ContextWindow         int
	ContextPercent        float64
	ContextScope          string
	ContextStrategy       string
	ContextActiveMessages int
	ContextSummaryCount   int
	ContextCompactedCount int
	ContextKeptCount      int

	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64
	BudgetLimitUSD        float64

	OverallCacheCapable    bool
	OverallRecentCacheRead int
	OverallRecentInput     int
	OverallRecentSamples   int
	TotalCacheBreaks       int

	MissingAssistantUsage int

	CachePerAgent []AgentCacheStat
	CachePerModel []AgentCacheStat

	Premise          string
	Outline          []OutlineSnapshot
	Characters       []string
	SupportingCount  int
	RecentSupporting []string
	Layered          bool
	CurrentVolumeArc string
	NextVolumeTitle  string
	CompassDirection string
	CompassScale     string

	LastCommitSummary  string
	LastReviewSummary  string
	LastCheckpointName string
	RecentSummaries    []string
}

type OutlineSnapshot struct {
	Chapter   int
	Title     string
	CoreEvent string
}

type AgentSnapshot struct {
	Name      string
	State     string
	TaskID    string
	TaskKind  string
	Summary   string
	Tool      string
	Turn      int
	Context   AgentContextSnapshot
	UpdatedAt time.Time
}

//
//
type AgentCacheStat struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

type AgentContextSnapshot struct {
	Tokens          int
	ContextWindow   int
	Percent         float64
	Scope           string
	Strategy        string
	ActiveMessages  int
	SummaryMessages int
	CompactedCount  int
	KeptCount       int
}

type CoCreateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CoCreateReply struct {
	Message     string
	Prompt      string
	Ready       bool
	Suggestions []string
	Raw         string
}

func ReplayDeltaText(item domain.RuntimeQueueItem) string {
	if payload, ok := item.Payload.(map[string]any); ok {
		if text, ok := payload["delta"].(string); ok {
			return text
		}
	}
	return ""
}
