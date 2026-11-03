package notice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const PipelineSchemaVersion = 2
const PipelineMaxObjectBytes = 24 << 20
const PipelinePolicy = "notice-v2-astra-low-partitions-6"

var ErrPipelineConflict = errors.New("notice pipeline input or lease conflict")
var ErrPipelineDisabled = errors.New("notice pipeline is disabled")
var ErrPipelineInvalid = errors.New("invalid notice pipeline request")
var ErrPipelineInputBudget = errors.New("notice model input exceeds review budget")

type PipelineStore interface {
	NoticePipeline(context.Context, PipelineRequest) (PipelineResponse, error)
}

// Operation is assigned by the transport, never selected by a JSON body.
type PipelineRequest struct {
	Operation        string              `json:"-"`
	RequestID        string              `json:"request_id,omitempty"`
	Exchange         string              `json:"exchange,omitempty"`
	WorkerID         string              `json:"worker_id,omitempty"`
	Stage            string              `json:"stage,omitempty"`
	JobID            string              `json:"job_id,omitempty"`
	LeaseToken       string              `json:"lease_token,omitempty"`
	SourceGeneration int64               `json:"source_generation,omitempty"`
	ObjectID         string              `json:"object_id,omitempty"`
	Object           *PipelineObject     `json:"object,omitempty"`
	Source           *PipelineSource     `json:"source,omitempty"`
	Page             *PipelinePage       `json:"page,omitempty"`
	Revision         *PipelineRevision   `json:"revision,omitempty"`
	Derivation       *PipelineDerivation `json:"derivation,omitempty"`
	ErrorCode        string              `json:"error_code,omitempty"`
	OCR              *PipelineOCR        `json:"ocr,omitempty"`
}

type PipelineResponse struct {
	Policy            string              `json:"policy,omitempty"`
	Sources           []PipelineSource    `json:"sources,omitempty"`
	Items             []Item              `json:"items,omitempty"`
	SchemaVersion     int                 `json:"schema_version"`
	Job               *PipelineJob        `json:"job,omitempty"`
	Object            *PipelineObject     `json:"object,omitempty"`
	Revision          *PipelineRevision   `json:"revision,omitempty"`
	Derivation        *PipelineDerivation `json:"derivation,omitempty"`
	Source            *PipelineSource     `json:"source,omitempty"`
	SavedID           string              `json:"saved_id,omitempty"`
	ProjectionCurrent bool                `json:"projection_current"`
}

type PipelineQuality struct {
	ReviewedPartitions int      `json:"reviewed_partitions"`
	TotalPartitions    int      `json:"total_partitions"`
	SectionSummaries   []string `json:"section_summaries,omitempty"`
	OCRState           string   `json:"ocr_state,omitempty"`
	SchemaVersion      int      `json:"schema_version"`
	RevisionID         *string  `json:"revision_id"`
	FactSetID          *string  `json:"fact_set_id"`
	SummaryID          *string  `json:"summary_id"`
	BodyState          string   `json:"body_state"`
	FactsState         string   `json:"facts_state"`
	SummaryState       string   `json:"summary_state"`
	Freshness          string   `json:"freshness"`
	AnswerableScope    string   `json:"answerable_scope"`
	RequiredConditions []string `json:"required_conditions"`
	HasMoreConditions  bool     `json:"has_more_conditions"`
}

type PipelineSource struct {
	Exchange      string `json:"exchange"`
	Enabled       bool   `json:"enabled"`
	CollectorMode string `json:"collector_mode"`
	ReadMode      string `json:"read_mode"`
	Generation    int64  `json:"generation"`
}

type PipelineJob struct {
	ID          string    `json:"id"`
	Exchange    string    `json:"exchange"`
	NoticeID    string    `json:"notice_id,omitempty"`
	Stage       string    `json:"stage"`
	RevisionID  string    `json:"revision_id,omitempty"`
	InputDigest string    `json:"input_digest"`
	Generation  int64     `json:"generation"`
	LeaseToken  string    `json:"lease_token"`
	LeaseUntil  time.Time `json:"lease_until"`
	Attempt     int       `json:"attempt"`
	Item        *Item     `json:"item,omitempty"`
}

type PipelineObject struct {
	ID        string `json:"id,omitempty"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Bytes     []byte `json:"bytes,omitempty"`
}

type PipelinePage struct {
	Ordinal   int      `json:"ordinal"`
	ObjectIDs []string `json:"object_ids"`
	Items     []Item   `json:"items"`
	Complete  bool     `json:"complete"`
}

type PipelineMedia struct {
	URL       string `json:"url"`
	ObjectID  string `json:"object_id,omitempty"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}

type PipelineRevision struct {
	BodyMarkupObjectID string          `json:"body_markup_object_id,omitempty"`
	ID                 string          `json:"id,omitempty"`
	Item               Item            `json:"item"`
	ObjectIDs          []string        `json:"object_ids"`
	Media              []PipelineMedia `json:"media"`
	Availability       string          `json:"availability"`
	Blocks             []PipelineBlock `json:"blocks"`
}

// OCR is a separate, fallible transcription, never the original body.
type PipelineOCR struct {
	RevisionID   string           `json:"revision_id"`
	Engine       string           `json:"engine"`
	Status       string           `json:"status"`
	Rows         []map[string]any `json:"rows"`
	RawObjectIDs []string         `json:"raw_object_ids"`
}

type PipelineBlock struct {
	SourceStart int    `json:"source_start_byte"`
	SourceEnd   int    `json:"source_end_byte"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	Start       int    `json:"start_byte"`
	End         int    `json:"end_byte"`
}

type PipelineEvidence struct {
	BlockID string `json:"block_id"`
	Quote   string `json:"quote"`
}

type PipelineFact struct {
	ConditionLogic *PipelineCondition `json:"condition_logic,omitempty"`
	ID             string             `json:"id"`
	Subject        string             `json:"subject"`
	Action         string             `json:"action"`
	Direction      string             `json:"direction"`
	Polarity       string             `json:"polarity"`
	Assertion      string             `json:"assertion"`
	Assets         []string           `json:"assets"`
	AssetScope     string             `json:"asset_scope"`
	Network        *string            `json:"network"`
	Market         *string            `json:"market"`
	TimeRole       string             `json:"time_role"`
	TimeRaw        *string            `json:"time_raw"`
	TimeStart      *string            `json:"time_start"`
	TimeEnd        *string            `json:"time_end"`
	Precision      string             `json:"precision"`
	Conditions     []string           `json:"conditions"`
	Exceptions     []string           `json:"exceptions"`
	Evidence       []PipelineEvidence `json:"evidence"`
}

type PipelineCondition struct {
	Operator string              `json:"operator"`
	Text     string              `json:"text,omitempty"`
	Children []PipelineCondition `json:"children,omitempty"`
}

type PipelineSummaryLine struct {
	Text    string   `json:"text"`
	FactIDs []string `json:"fact_ids"`
}

type PipelineAudit struct {
	InputDigest       string   `json:"input_digest"`
	ReviewedBlockIDs  []string `json:"reviewed_block_ids"`
	FactsVerdict      string   `json:"facts_verdict"`
	SummaryVerdict    string   `json:"summary_verdict"`
	MissingFacts      []string `json:"missing_facts"`
	MissingConditions []string `json:"missing_conditions"`
	Reason            string   `json:"reason"`
}

type PipelineDerivation struct {
	Partition          *PipelinePartition    `json:"partition,omitempty"`
	ID                 string                `json:"id,omitempty"`
	RevisionID         string                `json:"revision_id"`
	Policy             string                `json:"policy"`
	Facts              []PipelineFact        `json:"facts"`
	Summary            []PipelineSummaryLine `json:"summary"`
	RequiredConditions []string              `json:"required_conditions"`
	Audit              *PipelineAudit        `json:"audit,omitempty"`
	RawObjectIDs       []string              `json:"raw_object_ids"`
	Provider           string                `json:"provider"`
	Model              string                `json:"model"`
	Status             string                `json:"status"`
}

func PipelineID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("secure notice id: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func PipelineDigest(value any) string {
	data, _ := json.Marshal(value)
	return PipelineBytesDigest(data)
}

func PipelineBytesDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Public versions are opaque, positive JS-safe integers. Include the V2
// representation so the original-evidence RAG index cannot retain old text
// merely because the legacy tables' timestamp did not change.
func PipelineSnapshotVersion(legacy int64, items []Item) int64 {
	raw, _ := json.Marshal(struct {
		Legacy int64
		Items  []Item
	}{legacy, items})
	sum := sha256.Sum256(raw)
	return int64(binary.BigEndian.Uint64(sum[:8])>>12) + 1
}
