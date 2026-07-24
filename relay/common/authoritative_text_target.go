package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/QuantumNous/new-api/relaykit/types"
)

type AuthoritativeTextTarget struct {
	Model          string
	Protocol       types.RelayFormat
	RelayMode      int
	RequestURLPath string
}

type authoritativeTextTargetSeal struct {
	target               AuthoritativeTextTarget
	requestURLPathDigest string
	bodyDigest           string
	bodySize             int64
}

// AuthoritativeTextRequestPath returns only the escaped path that may be
// exposed to a credential-free target resolver.
func AuthoritativeTextRequestPath(requestURLPath string) (string, error) {
	parsed, err := url.ParseRequestURI(requestURLPath)
	if err != nil {
		return "", fmt.Errorf("parse authoritative text request path: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return "", fmt.Errorf("authoritative text request path must be relative")
	}
	path := parsed.EscapedPath()
	if path == "" {
		return "", fmt.Errorf("authoritative text request path is empty")
	}
	return path, nil
}

func authoritativeTextRequestPathDigest(requestURLPath string) string {
	digest := sha256.Sum256([]byte(requestURLPath))
	return hex.EncodeToString(digest[:])
}

func (info *RelayInfo) SealAuthoritativeTextTarget(target AuthoritativeTextTarget, request *PreparedRelayRequest) error {
	if info == nil || request == nil {
		return fmt.Errorf("relay info and prepared request are required")
	}
	if target.Model == "" || target.Protocol == "" {
		return fmt.Errorf("authoritative text target is incomplete")
	}
	requestPath, err := AuthoritativeTextRequestPath(info.RequestURLPath)
	if err != nil {
		return err
	}
	if target.RequestURLPath != requestPath {
		return fmt.Errorf("request path does not match authoritative target")
	}
	if request.Model() != target.Model {
		return fmt.Errorf("prepared request model %q does not match target model %q", request.Model(), target.Model)
	}
	if target.Protocol != types.RelayFormatGemini && !request.ModelFromBody() {
		return fmt.Errorf("prepared request body does not contain the authoritative target model")
	}
	if request.Protocol() != target.Protocol {
		return fmt.Errorf("prepared request protocol %q does not match target protocol %q", request.Protocol(), target.Protocol)
	}
	if info.RelayMode != target.RelayMode {
		return fmt.Errorf("relay mode does not match authoritative target")
	}
	info.authoritativeTextTargetSeal = &authoritativeTextTargetSeal{
		target:               target,
		requestURLPathDigest: authoritativeTextRequestPathDigest(info.RequestURLPath),
		bodyDigest:           request.BodyDigest(),
		bodySize:             request.Size(),
	}
	return info.ValidateAuthoritativeTextTarget()
}

func (info *RelayInfo) ClearAuthoritativeTextTarget() {
	if info != nil {
		info.authoritativeTextTargetSeal = nil
	}
}

func (info *RelayInfo) ValidateAuthoritativeTextTarget() error {
	if info == nil || info.authoritativeTextTargetSeal == nil {
		return nil
	}
	seal := info.authoritativeTextTargetSeal
	if info.UpstreamModelName != seal.target.Model || info.FinalRequestModel != seal.target.Model {
		return fmt.Errorf("authoritative text target model changed after preparation")
	}
	if info.GetFinalRequestRelayFormat() != seal.target.Protocol || info.FinalRequestRelayFormat != seal.target.Protocol {
		return fmt.Errorf("authoritative text target protocol changed after preparation")
	}
	if info.RelayMode != seal.target.RelayMode {
		return fmt.Errorf("authoritative text target relay mode changed after preparation")
	}
	requestPath, err := AuthoritativeTextRequestPath(info.RequestURLPath)
	if err != nil {
		return fmt.Errorf("authoritative text request path changed after preparation: %w", err)
	}
	if requestPath != seal.target.RequestURLPath || authoritativeTextRequestPathDigest(info.RequestURLPath) != seal.requestURLPathDigest {
		return fmt.Errorf("authoritative text target request path changed after preparation")
	}
	if info.FinalRequestBodyDigest != seal.bodyDigest || info.FinalRequestBodySize != seal.bodySize || info.UpstreamRequestBodySize != seal.bodySize {
		return fmt.Errorf("authoritative text request snapshot changed after preparation")
	}
	return nil
}
