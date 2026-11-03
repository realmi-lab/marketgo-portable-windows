package official

import "time"

// RestoreRemoteSnapshot mirrors collected evidence; freshness retains the
// producer collection timestamp instead of pretending replay is collection.
func (s *TickerService) RestoreRemoteSnapshot(value DomesticTickerSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = cloneTickerSummary(value)
	s.collectedAt = time.Unix(0, int64(value.AsOf*1e9))
}
func (s *AlignedCandleService) RestoreRemoteSnapshot(value DomesticTickerSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = cloneTickerSummary(value)

}
func (s *BinanceTickerService) RestoreRemoteSnapshot(value BinanceTicker24hSummary) {
	s.store(cloneBinanceTicker24hSummary(value), time.Unix(0, int64(value.AsOf*1e9)))
}
