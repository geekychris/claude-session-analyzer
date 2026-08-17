package store

// Types returned across the Wails boundary. Keep these plain JSON-serializable
// structs — no pointers to internal state, no non-nil zero-value gotchas.

type Project struct {
	Slug         string `json:"slug"`
	CWD          string `json:"cwd"`
	DisplayName  string `json:"displayName"`
	SessionCount int    `json:"sessionCount"`
	FileCount    int    `json:"fileCount"`
	LastSeenAt   string `json:"lastSeenAt"`

	// Aggregates
	TotalMessages       int   `json:"totalMessages"`
	TotalToolCalls      int   `json:"totalToolCalls"`
	TotalEdits          int   `json:"totalEdits"`
	TotalInputTokens    int64 `json:"totalInputTokens"`
	TotalOutputTokens   int64 `json:"totalOutputTokens"`
	TotalCacheReadTokens     int64 `json:"totalCacheReadTokens"`
	TotalCacheCreationTokens int64 `json:"totalCacheCreationTokens"`
	EstimatedCostUSD         float64 `json:"estimatedCostUsd"`
}

type Session struct {
	SessionID    string `json:"sessionId"`
	ProjectSlug  string `json:"projectSlug"`
	CWD          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	AITitle      string `json:"aiTitle"`
	StartedAt    string `json:"startedAt"`
	EndedAt      string `json:"endedAt"`
	MessageCount    int   `json:"messageCount"`
	ToolCallCount   int   `json:"toolCallCount"`
	EditCount       int   `json:"editCount"`
	EpisodeCount    int   `json:"episodeCount"`
	TotalInputTokens         int64 `json:"totalInputTokens"`
	TotalOutputTokens        int64 `json:"totalOutputTokens"`
	TotalCacheReadTokens     int64 `json:"totalCacheReadTokens"`
	TotalCacheCreationTokens int64 `json:"totalCacheCreationTokens"`
	SourcePath string `json:"sourcePath"`
	SizeBytes  int64  `json:"sizeBytes"`
	BytesIngested int64 `json:"bytesIngested"`

	PrimaryModel     string  `json:"primaryModel"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
}

type Episode struct {
	ID                  int64  `json:"id"`
	SessionID           string `json:"sessionId"`
	Seq                 int    `json:"seq"`
	UserMessageID       int64  `json:"userMessageId"`
	FirstEditToolCallID int64  `json:"firstEditToolCallId"`
	Ended               bool   `json:"ended"`
	UserPromptExcerpt   string `json:"userPromptExcerpt"`
	StartedAt           string `json:"startedAt"`
	FirstEditAt         string `json:"firstEditAt"`
	DurationMs          int64  `json:"durationMs"`
	ToolCallCount       int    `json:"toolCallCount"`
	FilesReadCount      int    `json:"filesReadCount"`
	FilesReadJSON       string `json:"filesReadJson"`
	BashCount           int    `json:"bashCount"`
	SearchCount         int    `json:"searchCount"`
	SubagentCount       int    `json:"subagentCount"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	EditFilePath        string `json:"editFilePath"`

	// Depth / cost derivations
	BytesReadBeforeEdit    int64  `json:"bytesReadBeforeEdit"`
	IntermediateArtifactCount int    `json:"intermediateArtifactCount"`
	IntermediateArtifactsJSON string `json:"intermediateArtifactsJson"`
	CostUSD                float64 `json:"costUsd"`
}

type Message struct {
	ID                  int64  `json:"id"`
	SessionID           string `json:"sessionId"`
	UUID                string `json:"uuid"`
	ParentUUID          string `json:"parentUuid"`
	Seq                 int    `json:"seq"`
	Role                string `json:"role"`
	EventType           string `json:"eventType"`
	EventSubtype        string `json:"eventSubtype"`
	Ts                  string `json:"ts"`
	Model               string `json:"model"`
	TextContent         string `json:"textContent"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	IsSidechain         bool   `json:"isSidechain"`
	IsMeta              bool   `json:"isMeta"`
}

type ToolCall struct {
	ID            int64  `json:"id"`
	SessionID     string `json:"sessionId"`
	MessageID     int64  `json:"messageId"`
	Seq           int    `json:"seq"`
	ToolUseID     string `json:"toolUseId"`
	Name          string `json:"name"`
	InputJSON     string `json:"inputJson"`
	FilePath      string `json:"filePath"`
	IsModifying   bool   `json:"isModifying"`
	IsRead        bool   `json:"isRead"`
	IsBash        bool   `json:"isBash"`
	IsSearch      bool   `json:"isSearch"`
	IsSubagent    bool   `json:"isSubagent"`
	Ts            string `json:"ts"`
	ResultSize    int64  `json:"resultSize"`
	ResultIsError bool   `json:"resultIsError"`
	EpisodeID     int64  `json:"episodeId"`
}

type Watermark struct {
	ID          int64  `json:"id"`
	SessionID   string `json:"sessionId"`
	MessageUUID string `json:"messageUuid"`
	Seq         int    `json:"seq"`
	Label       string `json:"label"`
	Note        string `json:"note"`
	CreatedAt   string `json:"createdAt"`
}

// ToolBreakdown counts how often each tool was invoked in a given scope.
type ToolBreakdown struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	ModifyingCount int    `json:"modifyingCount"`
	ErrorCount     int    `json:"errorCount"`
	TotalResultBytes int64 `json:"totalResultBytes"`
}

// FileTouchStat is one entry in "top files read/edited" reports.
type FileTouchStat struct {
	FilePath    string `json:"filePath"`
	ReadCount   int    `json:"readCount"`
	EditCount   int    `json:"editCount"`
	TotalCount  int    `json:"totalCount"`
	Sessions    int    `json:"sessions"`
}

// FileBlastRadius extends FileTouchStat with cross-cutting metrics:
// how many *distinct* projects and episodes touched the file, and the
// average number of *other* files edited in the same episodes as this one.
// A file with a high avgSiblingEdits is one that tends to cascade — editing
// it forces edits elsewhere too.
type FileBlastRadius struct {
	FilePath         string  `json:"filePath"`
	ReadCount        int     `json:"readCount"`
	EditCount        int     `json:"editCount"`
	Sessions         int     `json:"sessions"`
	Projects         int     `json:"projects"`
	Episodes         int     `json:"episodes"`
	AvgSiblingEdits  float64 `json:"avgSiblingEdits"`
	FirstSeen        string  `json:"firstSeen"`
	LastSeen         string  `json:"lastSeen"`
}

// BashBreakdown counts how often each (program, action) pair appears.
type BashBreakdownRow struct {
	Program     string `json:"program"`
	Action      string `json:"action"`
	Count       int    `json:"count"`
	ErrorCount  int    `json:"errorCount"`
	Sessions    int    `json:"sessions"`
}

// BashCommandSample is one specific command execution (for drill-in from a
// program/action row).
type BashCommandSample struct {
	ID          int64  `json:"id"`
	SessionID   string `json:"sessionId"`
	Seq         int    `json:"seq"`
	Ts          string `json:"ts"`
	Command     string `json:"command"`     // extracted from input_json
	Description string `json:"description"` // if the caller provided one
	ResultBytes int64  `json:"resultBytes"`
	IsError     bool   `json:"isError"`
	EpisodeID   int64  `json:"episodeId"`
}

// EpisodeBucket buckets episodes by tool_call_count for a distribution view.
type EpisodeBucket struct {
	Label string `json:"label"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Count int    `json:"count"`
}

// IngestReport summarises one ingestion pass.
type IngestReport struct {
	FilesScanned  int      `json:"filesScanned"`
	FilesIngested int      `json:"filesIngested"`
	BytesRead     int64    `json:"bytesRead"`
	MessagesAdded int      `json:"messagesAdded"`
	ToolCallsAdded int     `json:"toolCallsAdded"`
	EpisodesBuilt int      `json:"episodesBuilt"`
	DurationMs    int64    `json:"durationMs"`
	Errors        []string `json:"errors"`
}
