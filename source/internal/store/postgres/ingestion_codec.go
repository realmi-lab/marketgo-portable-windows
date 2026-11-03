package postgres

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"market-share-go/internal/ingestlog"
)

// The codec is a storage format, not a new exchange/parser contract. JSON
// numbers, unknown fields, optional zeroes and raw bytes all survive. Batches
// amortize repeated JSON keys without introducing inter-batch dependencies.
const compactIngestionVersion = 1
const compactIngestionMaxBytes = 16 << 20

var errCompactIngestionCapacity = errors.New("compact ingestion batch exceeds bounded codec capacity")

type compactIngestionBlock struct {
	Replay, Raw         []byte
	ReplayHash, RawHash [32]byte
}

type compactIngestionPointer struct {
	Version int   `json:"CompactV"`
	Day     int64 `json:"CompactDay"`
	Block   int64 `json:"CompactBlock"`
}

var compactGzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

func compressIngestionBytes(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	w := compactGzipPool.Get().(*gzip.Writer)
	defer func() { w.Reset(io.Discard); compactGzipPool.Put(w) }()
	w.Reset(&out)
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func expandIngestionBytes(encoded, hash []byte) ([]byte, error) {
	if len(encoded) > compactIngestionMaxBytes || len(hash) != sha256.Size {
		return nil, errors.New("invalid compact ingestion size or checksum")
	}
	actual := sha256.Sum256(encoded)
	if !bytes.Equal(actual[:], hash) {
		return nil, errors.New("compact ingestion checksum mismatch")
	}
	input := bytes.NewReader(encoded)
	r, err := gzip.NewReader(input)
	if err != nil {
		return nil, err
	}
	r.Multistream(false)
	raw, err := io.ReadAll(io.LimitReader(r, compactIngestionMaxBytes+1))
	closeErr := r.Close()
	if err != nil || closeErr != nil || len(raw) > compactIngestionMaxBytes || input.Len() != 0 {
		return nil, errors.New("invalid or oversized compact ingestion stream")
	}
	return raw, nil
}

func appendCompactRecord(out *bytes.Buffer, cursor int64, value []byte) {
	out.Write(binary.AppendUvarint(nil, uint64(cursor)))
	out.Write(binary.AppendUvarint(nil, uint64(len(value))))
	out.Write(value)
}

func readCompactRecords(data []byte) (map[int64][]byte, error) {
	result := make(map[int64][]byte)
	for len(data) > 0 {
		cursor, n := binary.Uvarint(data)
		if n <= 0 || cursor == 0 || cursor > 1<<63-1 {
			return nil, errors.New("invalid compact ingestion cursor")
		}
		data = data[n:]
		size, n := binary.Uvarint(data)
		if n <= 0 || size > uint64(len(data)-n) {
			return nil, errors.New("truncated compact ingestion record")
		}
		data = data[n:]
		if _, exists := result[int64(cursor)]; exists || len(result) >= ingestlog.MaxBatch {
			return nil, errors.New("duplicate or excessive compact ingestion records")
		}
		result[int64(cursor)] = data[:int(size)]
		data = data[int(size):]
	}
	return result, nil
}

// splitCompactFrame leaves the complete parsed message in replay. The raw
// record starts with a presence tag: absent, null and empty bytes are distinct.
func splitCompactFrame(payload []byte) ([]byte, []byte, error) {
	var envelope, frame map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(envelope["Frame"], &frame); err != nil || frame == nil {
		return nil, nil, errors.New("compact frame is missing Frame")
	}
	raw := []byte{0}
	if value, exists := frame["Payload"]; exists {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			raw[0] = 1
		} else {
			var data []byte
			if err := json.Unmarshal(value, &data); err != nil {
				return nil, nil, err
			}
			raw = append([]byte{2}, data...)
		}
	}
	delete(frame, "Payload")
	envelope["Frame"], _ = json.Marshal(frame)
	replay, err := json.Marshal(envelope)
	return replay, raw, err
}

func restoreCompactFrame(replay, raw []byte) ([]byte, error) {
	if len(raw) == 0 || raw[0] > 2 || raw[0] < 2 && len(raw) != 1 {
		return nil, errors.New("invalid compact raw presence tag")
	}
	var envelope, frame map[string]json.RawMessage
	if err := json.Unmarshal(replay, &envelope); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(envelope["Frame"], &frame); err != nil || frame == nil {
		return nil, errors.New("compact replay is missing Frame")
	}
	switch raw[0] {
	case 1:
		frame["Payload"] = json.RawMessage("null")
	case 2:
		frame["Payload"], _ = json.Marshal(append([]byte{}, raw[1:]...))
	}
	envelope["Frame"], _ = json.Marshal(frame)
	return json.Marshal(envelope)
}

func encodeCompactIngestion(events []ingestlog.Event) (compactIngestionBlock, error) {
	var block compactIngestionBlock
	var replay, raw bytes.Buffer
	if len(events) == 0 || len(events) > ingestlog.MaxBatch {
		return block, errors.New("invalid compact ingestion batch size")
	}
	seen := make(map[int64]bool, len(events))
	for _, event := range events {
		if event.Cursor <= 0 || seen[event.Cursor] {
			return block, errors.New("invalid compact ingestion identity")
		}
		seen[event.Cursor] = true
		value := []byte(event.Payload)
		if !json.Valid(value) {
			return block, errors.New("invalid compact ingestion JSON")
		}
		if event.Kind == "frame" {
			var original []byte
			var err error
			value, original, err = splitCompactFrame(value)
			if err != nil {
				return block, err
			}
			appendCompactRecord(&raw, event.Cursor, original)
		}
		appendCompactRecord(&replay, event.Cursor, value)
		if replay.Len() > compactIngestionMaxBytes || raw.Len() > compactIngestionMaxBytes {
			return block, errCompactIngestionCapacity
		}
	}
	var err error
	block.Replay, err = compressIngestionBytes(replay.Bytes())
	if err != nil {
		return block, err
	}
	block.Raw, err = compressIngestionBytes(raw.Bytes())
	if len(block.Replay) > compactIngestionMaxBytes || len(block.Raw) > compactIngestionMaxBytes {
		return block, errCompactIngestionCapacity
	}
	block.ReplayHash = sha256.Sum256(block.Replay)
	block.RawHash = sha256.Sum256(block.Raw)
	return block, err
}

func compactIngestionHeader(event ingestlog.Event, day, block int64, allocation int) ([]byte, error) {
	var original map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &original); err != nil {
		return nil, err
	}
	header := map[string]any{"CompactV": compactIngestionVersion, "CompactDay": day, "CompactBlock": block, "CompactBytes": allocation}
	if source, exists := original["Source"]; exists {
		header["Source"] = source
	}
	if event.Kind == "frame" {
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(original["Frame"], &frame); err != nil {
			return nil, err
		}
		metadata := make(map[string]json.RawMessage)
		for _, key := range []string{"Exchange", "Market", "FrameClass", "MessageType"} {
			if value, exists := frame[key]; exists {
				metadata[key] = value
			}
		}
		header["Frame"] = metadata
	}
	return json.Marshal(header)
}

func compactPointer(payload []byte) (compactIngestionPointer, error) {
	var pointer compactIngestionPointer
	if err := json.Unmarshal(payload, &pointer); err != nil {
		return pointer, err
	}
	if pointer.Version != 0 && (pointer.Version != compactIngestionVersion || pointer.Day < 0 || pointer.Block <= 0) {
		return pointer, fmt.Errorf("unsupported compact ingestion pointer: version %d", pointer.Version)
	}
	return pointer, nil
}
