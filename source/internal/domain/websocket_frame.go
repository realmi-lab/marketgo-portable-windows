package domain

// WebSocketFrame is the exact application frame received from one exchange
// connection. Payload remains byte-for-byte identical to the data returned by
// the WebSocket client. The collector parses once to attach journal policy
// metadata; selected frames are not applied to runtime state before commit.
type WebSocketFrame struct {
	Exchange     Exchange
	ConnectionID string
	Sequence     uint64
	MessageType  int16
	ReceivedAtNS int64
	Payload      []byte
	// Parser metadata only: TRADE is not a claim of canonical acceptance,
	// deduplication or complete observation. Empty market means unknown/multiple.
	FrameClass string
	Market     string
}

const (
	FrameUnknown           = "UNKNOWN"
	FrameTrade             = "TRADE"
	FramePartialTrade      = "PARTIAL_TRADE"
	FrameParseError        = "PARSE_ERROR"
	FrameSubscriptionError = "SUBSCRIPTION_ERROR"
	FrameSnapshot          = "SNAPSHOT"
	FrameOther             = "OTHER"
	// WebSocketFrameMarketMaxBytes bounds the persisted statistics key. Empty
	// remains meaningful when one frame has no safe single-market attribution.
	WebSocketFrameMarketMaxBytes = 128
)

func ValidWebSocketFrameClass(value string) bool {
	switch value {
	case FrameUnknown, FrameTrade, FramePartialTrade, FrameParseError,
		FrameSubscriptionError, FrameSnapshot, FrameOther:
		return true
	default:
		return false
	}
}

// FrameReceipt identifies one accepted position in the process-wide durable
// frame journal. Zero means no persistence barrier is required, including a
// parsed order-book frame handled by the independent calculated-state writer.
type FrameReceipt uint64
