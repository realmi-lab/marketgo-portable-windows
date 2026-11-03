//go:build rustffi && cgo && (linux || windows)

package aggregate

/*
#cgo CFLAGS: -I${SRCDIR}/../../rust/rolling-core/include
#cgo linux LDFLAGS: -L${SRCDIR}/../../rust/rolling-core/target/release -lmarket_share_rolling -ldl -lpthread -lm
#cgo windows LDFLAGS: -L${SRCDIR}/../../rust/rolling-core/target/release -lmarket_share_rolling
#include "market_share_rolling.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

const (
	rustStatusOK                   = 0
	rustStatusUnsupportedWindow    = 3
	rustStatusInsufficientCapacity = 4
)

type rustRollingEngine struct {
	handle  *C.ms_engine
	keyToID map[VolumeKey]uint64
	idToKey map[uint64]VolumeKey
	nextID  uint64
}

func newRollingEngine(windows []int64) (rollingEngine, error) {
	if len(windows) == 0 {
		return nil, errors.New("rust rolling engine requires at least one window")
	}
	if C.ms_rolling_abi_version() != C.uint32_t(RustRollingABIVersion) {
		return nil, errors.New("rust rolling engine ABI version mismatch")
	}
	if status := int32(C.ms_rolling_self_test()); status != rustStatusOK {
		return nil, engineError("self-test", status)
	}
	cWindows := make([]C.int64_t, len(windows))
	for index, window := range windows {
		cWindows[index] = C.int64_t(window)
	}
	var handle *C.ms_engine
	status := int32(C.ms_engine_new(
		(*C.int64_t)(unsafe.Pointer(&cWindows[0])), C.size_t(len(cWindows)), &handle,
	))
	if status != rustStatusOK || handle == nil {
		return nil, engineError("create", status)
	}
	engine := &rustRollingEngine{
		handle: handle, keyToID: make(map[VolumeKey]uint64),
		idToKey: make(map[uint64]VolumeKey), nextID: 1,
	}
	runtime.SetFinalizer(engine, (*rustRollingEngine).Close)
	return engine, nil
}

func (e *rustRollingEngine) keyID(key VolumeKey) uint64 {
	if id, ok := e.keyToID[key]; ok {
		return id
	}
	id := e.nextID
	e.nextID++
	e.keyToID[key] = id
	e.idToKey[id] = key
	return id
}

func rustTotals(keyID uint64, value Totals) C.ms_totals {
	return C.ms_totals{
		key_id: C.uint64_t(keyID), native_volume: C.double(value.NativeVolume),
		krw_volume: C.double(value.KRWVolume), trade_count: C.uint64_t(value.TradeCount),
		unresolved_count: C.uint64_t(value.UnresolvedCount),
		buy_krw_volume:   C.double(value.BuyKRWVolume),
		sell_krw_volume:  C.double(value.SellKRWVolume), base_volume: C.double(value.BaseVolume),
	}
}

func (e *rustRollingEngine) Ingest(eventSecond, currentSecond int64, key VolumeKey, value Totals) error {
	row := rustTotals(e.keyID(key), value)
	status := int32(C.ms_engine_ingest(e.handle, C.int64_t(eventSecond), C.int64_t(currentSecond), &row))
	runtime.KeepAlive(e)
	if status != rustStatusOK {
		return engineError("ingest", status)
	}
	return nil
}

func (e *rustRollingEngine) SubtractBatch(window int64, values map[VolumeKey]Totals) error {
	if len(values) == 0 {
		return nil
	}
	rows := make([]C.ms_totals, 0, len(values))
	for key, value := range values {
		id, ok := e.keyToID[key]
		if ok {
			rows = append(rows, rustTotals(id, value))
		}
	}
	if len(rows) == 0 {
		return nil
	}
	status := int32(C.ms_engine_subtract_many(
		e.handle, C.int64_t(window), &rows[0], C.size_t(len(rows)),
	))
	runtime.KeepAlive(e)
	if status != rustStatusOK {
		return engineError("subtract-batch", status)
	}
	return nil
}

func (e *rustRollingEngine) Snapshot(window int64) (map[VolumeKey]Totals, bool, error) {
	var count C.size_t
	status := int32(C.ms_engine_rows(e.handle, C.int64_t(window), nil, 0, &count))
	runtime.KeepAlive(e)
	if status == rustStatusUnsupportedWindow {
		return nil, false, nil
	}
	if status != rustStatusOK && status != rustStatusInsufficientCapacity {
		return nil, false, engineError("snapshot-size", status)
	}
	if count == 0 {
		return map[VolumeKey]Totals{}, true, nil
	}
	rows := make([]C.ms_totals, int(count))
	status = int32(C.ms_engine_rows(e.handle, C.int64_t(window), &rows[0], count, &count))
	runtime.KeepAlive(e)
	if status != rustStatusOK {
		return nil, false, engineError("snapshot", status)
	}
	result := make(map[VolumeKey]Totals, int(count))
	for _, row := range rows[:int(count)] {
		key, ok := e.idToKey[uint64(row.key_id)]
		if !ok {
			return nil, false, errors.New("rust rolling engine returned an unknown key")
		}
		result[key] = Totals{
			NativeVolume: float64(row.native_volume), KRWVolume: float64(row.krw_volume),
			TradeCount: uint64(row.trade_count), UnresolvedCount: uint64(row.unresolved_count),
			BuyKRWVolume: float64(row.buy_krw_volume), SellKRWVolume: float64(row.sell_krw_volume),
			BaseVolume: float64(row.base_volume),
		}
	}
	return result, true, nil
}

func (*rustRollingEngine) Name() string       { return C.GoString(C.ms_rolling_engine_name()) }
func (*rustRollingEngine) ABIVersion() uint32 { return uint32(C.ms_rolling_abi_version()) }

func (e *rustRollingEngine) Close() {
	if e == nil || e.handle == nil {
		return
	}
	C.ms_engine_free(e.handle)
	e.handle = nil
	runtime.SetFinalizer(e, nil)
}
