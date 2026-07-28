package evolution

import scenery "scenery.sh/internal/contract"

type ApprovalToken = scenery.ApprovalToken

type ApprovalVerifier func(token ApprovalToken, canonicalPayload []byte) error

func ApprovalTokenPayload(token ApprovalToken) ([]byte, error) {
	return scenery.ApprovalTokenPayload(token)
}
