package common

type BillingOperationLookupCandidate struct {
	LookupHMAC       string
	OwnerHMAC        string
	ConversationHMAC string
	KeyVersion       string
}

type BillingOperationIdentity struct {
	Candidates       []BillingOperationLookupCandidate
	ExpectedRevision uint64
	Purpose          string
	Fingerprint      string
}

type ManagedOutcomeBillingCheckpoint struct {
	OutcomeId               int64
	RequestFingerprint      string
	ExpectedPhase           string
	NextPhase               string
	ResponseStatus          int
	ResponseContentType     string
	ResponsePayload         []byte
	AssistantPayload        []byte
	SummaryExecutionPayload []byte
	SummaryResultPayload    []byte
	NextStatePayload        []byte
}
