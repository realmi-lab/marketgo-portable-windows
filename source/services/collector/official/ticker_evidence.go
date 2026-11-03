package official

import contract "market-share-go/contracts/collection/official"

const DomesticTickerEvidenceVersion = contract.DomesticTickerEvidenceVersion

func EncodeDomesticTickerEvidence(s DomesticTickerSummary) ([]byte, error) {
	return contract.EncodeDomesticTickerEvidence(s)
}
func DecodeDomesticTickerEvidence(b []byte) (DomesticTickerSummary, error) {
	return contract.DecodeDomesticTickerEvidence(b)
}
