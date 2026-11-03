package aggregate

import "fmt"

const RustRollingABIVersion uint32 = 1

type rollingEngine interface {
	Ingest(eventSecond, currentSecond int64, key VolumeKey, value Totals) error
	SubtractBatch(window int64, values map[VolumeKey]Totals) error
	Snapshot(window int64) (map[VolumeKey]Totals, bool, error)
	Name() string
	ABIVersion() uint32
	Close()
}

type EngineInfo struct {
	Name       string
	ABIVersion uint32
	Native     bool
}

func engineError(operation string, status int32) error {
	return fmt.Errorf("rust rolling engine %s failed with status %d", operation, status)
}
