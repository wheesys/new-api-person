package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	common2 "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// applyUpstreamContentLength populates req.ContentLength when the upstream
// body is wrapped in a BodyStorage (see relay/common/outbound_body.go).
//
// net/http.NewRequest only auto-detects ContentLength for *bytes.Reader,
// *bytes.Buffer and *strings.Reader. When the body is a type-erased io.Reader
// (which is the case for ReaderOnly(BodyStorage)), the Content-Length header
// would otherwise be omitted, forcing chunked transfer encoding and breaking
// some upstreams that require an explicit Content-Length.
func applyUpstreamContentLength(req *http.Request, info *common.RelayInfo) {
	if info == nil {
		return
	}
	if info.UpstreamRequestBodySize > 0 && req.ContentLength <= 0 {
		req.ContentLength = info.UpstreamRequestBodySize
	}
}

func validateAuthoritativeTextTarget(info *common.RelayInfo, stage string) error {
	if err := info.ValidateAuthoritativeTextTarget(); err != nil {
		return fmt.Errorf("authoritative text target validation failed %s: %w", stage, err)
	}
	return nil
}

func maskRequestURL(requestURL string) string {
	queryIndex := strings.IndexByte(requestURL, '?')
	if queryIndex >= 0 {
		query := requestURL[queryIndex+1:]
		if fragmentIndex := strings.IndexByte(query, '#'); fragmentIndex >= 0 {
			query = query[:fragmentIndex]
		}
		values, err := url.ParseQuery(query)
		if err != nil {
			requestURL = requestURL[:queryIndex] + "?***"
		} else {
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			maskedQuery := make([]string, 0, len(keys))
			for _, key := range keys {
				maskedQuery = append(maskedQuery, url.QueryEscape(key)+"=***")
			}
			requestURL = requestURL[:queryIndex]
			if len(maskedQuery) > 0 {
				requestURL += "?" + strings.Join(maskedQuery, "&")
			}
		}
	} else if fragmentIndex := strings.IndexByte(requestURL, '#'); fragmentIndex >= 0 {
		requestURL = requestURL[:fragmentIndex]
	}
	switch {
	case strings.HasPrefix(requestURL, "wss://"):
		masked := common2.MaskSensitiveInfo("https://" + strings.TrimPrefix(requestURL, "wss://"))
		return "wss://" + strings.TrimPrefix(masked, "https://")
	case strings.HasPrefix(requestURL, "ws://"):
		masked := common2.MaskSensitiveInfo("http://" + strings.TrimPrefix(requestURL, "ws://"))
		return "ws://" + strings.TrimPrefix(masked, "http://")
	default:
		return common2.MaskSensitiveInfo(requestURL)
	}
}

func maskRequestError(err error, requestURL string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if requestURL != "" {
		message = strings.ReplaceAll(message, requestURL, maskRequestURL(requestURL))
	}
	return errors.New(common2.MaskSensitiveInfo(message))
}

func SetupApiRequestHeader(info *common.RelayInfo, c *gin.Context, req *http.Header) {
	if info.RelayMode == constant.RelayModeAudioTranscription || info.RelayMode == constant.RelayModeAudioTranslation {
		// multipart/form-data
	} else if info.RelayMode == constant.RelayModeRealtime {
		// websocket
	} else {
		req.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		req.Set("Accept", c.Request.Header.Get("Accept"))
		if info.IsStream && c.Request.Header.Get("Accept") == "" {
			req.Set("Accept", "text/event-stream")
		}
	}
}

const clientHeaderPlaceholderPrefix = "{client_header:"

const (
	headerPassthroughAllKey        = "*"
	headerPassthroughRegexPrefix   = "re:"
	headerPassthroughRegexPrefixV2 = "regex:"
)

var gatewayContextHeaderNamesLower = map[string]struct{}{
	"x-new-api-context-id":              {},
	"x-new-api-context-mode":            {},
	"x-new-api-context-revision":        {},
	"x-new-api-context-idempotency-key": {},
}

var passthroughSkipHeaderNamesLower = map[string]struct{}{
	// RFC 7230 hop-by-hop headers.
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},

	"cookie": {},

	// Additional headers that should not be forwarded by name-matching passthrough rules.
	"host":            {},
	"content-length":  {},
	"accept-encoding": {},

	// Do not passthrough credentials by wildcard/regex.
	"authorization":  {},
	"x-api-key":      {},
	"x-goog-api-key": {},

	// Context consensus headers are consumed only by this gateway.
	"x-new-api-context-id":              {},
	"x-new-api-context-mode":            {},
	"x-new-api-context-revision":        {},
	"x-new-api-context-idempotency-key": {},

	// WebSocket handshake headers are generated by the client/dialer.
	"sec-websocket-key":        {},
	"sec-websocket-version":    {},
	"sec-websocket-extensions": {},
}

var headerPassthroughRegexCache sync.Map // map[string]*regexp.Regexp

func getHeaderPassthroughRegex(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("empty regex pattern")
	}
	if v, ok := headerPassthroughRegexCache.Load(pattern); ok {
		if re, ok := v.(*regexp.Regexp); ok {
			return re, nil
		}
		headerPassthroughRegexCache.Delete(pattern)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := headerPassthroughRegexCache.LoadOrStore(pattern, compiled)
	if re, ok := actual.(*regexp.Regexp); ok {
		return re, nil
	}
	return compiled, nil
}

func IsHeaderPassthroughRuleKey(key string) bool {
	return isHeaderPassthroughRuleKey(key)
}
func isHeaderPassthroughRuleKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if key == headerPassthroughAllKey {
		return true
	}
	lower := strings.ToLower(key)
	return strings.HasPrefix(lower, headerPassthroughRegexPrefix) || strings.HasPrefix(lower, headerPassthroughRegexPrefixV2)
}

func shouldSkipPassthroughHeader(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	lower := strings.ToLower(name)
	if _, ok := passthroughSkipHeaderNamesLower[lower]; ok {
		return true
	}
	return false
}

func applyHeaderOverridePlaceholders(template string, c *gin.Context, apiKey string) (string, bool, error) {
	trimmed := strings.TrimSpace(template)
	if strings.HasPrefix(trimmed, clientHeaderPlaceholderPrefix) {
		afterPrefix := trimmed[len(clientHeaderPlaceholderPrefix):]
		end := strings.Index(afterPrefix, "}")
		if end < 0 || end != len(afterPrefix)-1 {
			return "", false, fmt.Errorf("client_header placeholder must be the full value: %q", template)
		}

		name := strings.TrimSpace(afterPrefix[:end])
		if name == "" {
			return "", false, fmt.Errorf("client_header placeholder name is empty: %q", template)
		}
		if _, gatewayOnly := gatewayContextHeaderNamesLower[strings.ToLower(name)]; gatewayOnly {
			return "", false, nil
		}
		if c == nil || c.Request == nil {
			return "", false, fmt.Errorf("missing request context for client_header placeholder")
		}
		clientHeaderValue := c.Request.Header.Get(name)
		if strings.TrimSpace(clientHeaderValue) == "" {
			return "", false, nil
		}
		// Do not interpolate {api_key} inside client-supplied content.
		return clientHeaderValue, true, nil
	}

	if strings.Contains(template, "{api_key}") {
		template = strings.ReplaceAll(template, "{api_key}", apiKey)
	}
	if strings.TrimSpace(template) == "" {
		return "", false, nil
	}
	return template, true, nil
}

// processHeaderOverride applies channel header overrides, with placeholder substitution.
// Supported placeholders:
//   - {api_key}: resolved to the channel API key
//   - {client_header:<name>}: resolved to the incoming request header value
//
// Header passthrough rules (keys only; values are ignored):
//   - "*": passthrough all incoming headers by name (excluding unsafe headers)
//   - "re:<regex>" / "regex:<regex>": passthrough headers whose names match the regex (Go regexp)
//
// Passthrough rules are applied first, then normal overrides are applied, so explicit overrides win.
func processHeaderOverride(info *common.RelayInfo, c *gin.Context) (map[string]string, error) {
	headerOverride := make(map[string]string)
	if info == nil {
		return headerOverride, nil
	}

	headerOverrideSource := common.GetEffectiveHeaderOverride(info)

	passAll := false
	var passthroughRegex []*regexp.Regexp
	if !info.IsChannelTest {
		for k := range headerOverrideSource {
			key := strings.TrimSpace(strings.ToLower(k))
			if key == "" {
				continue
			}
			if key == headerPassthroughAllKey {
				passAll = true
				continue
			}

			var pattern string
			switch {
			case strings.HasPrefix(key, headerPassthroughRegexPrefix):
				pattern = strings.TrimSpace(key[len(headerPassthroughRegexPrefix):])
			case strings.HasPrefix(key, headerPassthroughRegexPrefixV2):
				pattern = strings.TrimSpace(key[len(headerPassthroughRegexPrefixV2):])
			default:
				continue
			}

			if pattern == "" {
				return nil, types.NewError(fmt.Errorf("header passthrough regex pattern is empty: %q", k), types.ErrorCodeChannelHeaderOverrideInvalid)
			}
			compiled, err := getHeaderPassthroughRegex(pattern)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeChannelHeaderOverrideInvalid)
			}
			passthroughRegex = append(passthroughRegex, compiled)
		}
	}

	if passAll || len(passthroughRegex) > 0 {
		if c == nil || c.Request == nil {
			return nil, types.NewError(fmt.Errorf("missing request context for header passthrough"), types.ErrorCodeChannelHeaderOverrideInvalid)
		}
		for name := range c.Request.Header {
			if shouldSkipPassthroughHeader(name) {
				continue
			}
			if !passAll {
				matched := false
				for _, re := range passthroughRegex {
					if re.MatchString(name) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			value := strings.TrimSpace(c.Request.Header.Get(name))
			if value == "" {
				continue
			}
			headerOverride[strings.ToLower(strings.TrimSpace(name))] = value
		}
	}

	for k, v := range headerOverrideSource {
		if isHeaderPassthroughRuleKey(k) {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(k))
		if key == "" {
			continue
		}
		if _, gatewayOnly := gatewayContextHeaderNamesLower[key]; gatewayOnly {
			continue
		}

		str, ok := v.(string)
		if !ok {
			return nil, types.NewError(nil, types.ErrorCodeChannelHeaderOverrideInvalid)
		}
		if info.IsChannelTest && strings.HasPrefix(strings.TrimSpace(str), clientHeaderPlaceholderPrefix) {
			continue
		}

		value, include, err := applyHeaderOverridePlaceholders(str, c, info.ApiKey)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeChannelHeaderOverrideInvalid)
		}
		if !include {
			continue
		}

		headerOverride[key] = value
	}
	return headerOverride, nil
}

func ResolveHeaderOverride(info *common.RelayInfo, c *gin.Context) (map[string]string, error) {
	return processHeaderOverride(info, c)
}

func applyHeaderOverrideToRequest(req *http.Request, headerOverride map[string]string) {
	if req == nil {
		return
	}
	for key, value := range headerOverride {
		if _, gatewayOnly := gatewayContextHeaderNamesLower[strings.ToLower(strings.TrimSpace(key))]; gatewayOnly {
			continue
		}
		req.Header.Set(key, value)
		// set Host in req
		if strings.EqualFold(key, "Host") {
			req.Host = value
		}
	}
	removeGatewayContextHeaders(req.Header)
}

func removeGatewayContextHeaders(header http.Header) {
	for headerName := range gatewayContextHeaderNamesLower {
		header.Del(headerName)
	}
}

func DoApiRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	if err := validateAuthoritativeTextTarget(info, "before request URL resolution"); err != nil {
		return nil, err
	}
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", maskRequestError(err, ""))
	}
	if err := validateAuthoritativeTextTarget(info, "after request URL resolution"); err != nil {
		return nil, err
	}
	logger.LogDebug(c, "fullRequestURL: %s", maskRequestURL(fullRequestURL))
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", maskRequestError(err, fullRequestURL))
	}
	applyUpstreamContentLength(req, info)
	headers := req.Header
	err = a.SetupRequestHeader(c, &headers, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	if err := validateAuthoritativeTextTarget(info, "after request header setup"); err != nil {
		return nil, err
	}
	// 在 SetupRequestHeader 之后应用 Header Override，确保用户设置优先级最高
	// 这样可以覆盖默认的 Authorization header 设置
	headerOverride, err := processHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	applyHeaderOverrideToRequest(req, headerOverride)
	if err := validateAuthoritativeTextTarget(info, "before network request"); err != nil {
		return nil, err
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func DoFormRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	if err := validateAuthoritativeTextTarget(info, "before request URL resolution"); err != nil {
		return nil, err
	}
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", maskRequestError(err, ""))
	}
	if err := validateAuthoritativeTextTarget(info, "after request URL resolution"); err != nil {
		return nil, err
	}
	logger.LogDebug(c, "fullRequestURL: %s", maskRequestURL(fullRequestURL))
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", maskRequestError(err, fullRequestURL))
	}
	applyUpstreamContentLength(req, info)
	// set form data
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	headers := req.Header
	err = a.SetupRequestHeader(c, &headers, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	if err := validateAuthoritativeTextTarget(info, "after request header setup"); err != nil {
		return nil, err
	}
	// 在 SetupRequestHeader 之后应用 Header Override，确保用户设置优先级最高
	// 这样可以覆盖默认的 Authorization header 设置
	headerOverride, err := processHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	applyHeaderOverrideToRequest(req, headerOverride)
	if err := validateAuthoritativeTextTarget(info, "before network request"); err != nil {
		return nil, err
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func DoWssRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*websocket.Conn, error) {
	if err := validateAuthoritativeTextTarget(info, "before request URL resolution"); err != nil {
		return nil, err
	}
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", maskRequestError(err, ""))
	}
	if err := validateAuthoritativeTextTarget(info, "after request URL resolution"); err != nil {
		return nil, err
	}
	targetHeader := http.Header{}
	err = a.SetupRequestHeader(c, &targetHeader, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	if err := validateAuthoritativeTextTarget(info, "after request header setup"); err != nil {
		return nil, err
	}
	// 在 SetupRequestHeader 之后应用 Header Override，确保用户设置优先级最高
	// 这样可以覆盖默认的 Authorization header 设置
	headerOverride, err := processHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		targetHeader.Set(key, value)
	}
	targetHeader.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	removeGatewayContextHeaders(targetHeader)
	if err := validateAuthoritativeTextTarget(info, "before network request"); err != nil {
		return nil, err
	}
	targetConn, _, err := websocket.DefaultDialer.Dial(fullRequestURL, targetHeader)
	if err != nil {
		return nil, fmt.Errorf("dial failed to %s: %w", maskRequestURL(fullRequestURL), maskRequestError(err, fullRequestURL))
	}
	// send request body
	//all, err := io.ReadAll(requestBody)
	//err = service.WssString(c, targetConn, string(all))
	return targetConn, nil
}

func startPingKeepAlive(c *gin.Context, pingInterval time.Duration) (context.CancelFunc, <-chan struct{}) {
	pingerCtx, stopPinger := context.WithCancel(context.Background())
	done := make(chan struct{})

	gopool.Go(func() {
		defer close(done)
		defer func() {
			// 增加panic恢复处理
			if r := recover(); r != nil {
				logger.LogDebug(c, "SSE ping goroutine panic recovered: %v", r)
			}
			logger.LogDebug(c, "SSE ping goroutine stopped")
		}()

		if pingInterval <= 0 {
			pingInterval = helper.DefaultPingInterval
		}

		ticker := time.NewTicker(pingInterval)
		// 确保在任何情况下都清理ticker
		defer func() {
			ticker.Stop()
			logger.LogDebug(c, "SSE ping ticker stopped")
		}()

		var pingMutex sync.Mutex
		logger.LogDebug(c, "SSE ping goroutine started")

		// 增加超时控制，防止goroutine长时间运行
		maxPingDuration := 120 * time.Minute // 最大ping持续时间
		pingTimeout := time.NewTimer(maxPingDuration)
		defer pingTimeout.Stop()

		for {
			select {
			// 发送 ping 数据
			case <-ticker.C:
				if err := sendPingData(c, &pingMutex); err != nil {
					logger.LogDebug(c, "SSE ping error, stopping goroutine: %s", err.Error())
					return
				}
			// 收到退出信号
			case <-pingerCtx.Done():
				return
			// request 结束
			case <-c.Request.Context().Done():
				return
			// 超时保护，防止goroutine无限运行
			case <-pingTimeout.C:
				logger.LogDebug(c, "SSE ping goroutine timeout, stopping")
				return
			}
		}
	})

	return stopPinger, done
}

func sendPingData(c *gin.Context, mutex *sync.Mutex) error {
	mutex.Lock()
	defer mutex.Unlock()

	// Bound the write so a slow client cannot block this goroutine forever;
	// doRequest's defer waits for the pinger to exit before returning.
	helper.ExtendWriteDeadline(c)
	err := helper.PingData(c)
	if err != nil {
		logger.LogError(c, "SSE ping error: "+err.Error())
		return err
	}

	logger.LogDebug(c, "SSE ping data sent")
	return nil
}

func DoRequest(c *gin.Context, req *http.Request, info *common.RelayInfo) (*http.Response, error) {
	return doRequest(c, req, info)
}
func doRequest(c *gin.Context, req *http.Request, info *common.RelayInfo) (*http.Response, error) {
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", maskRequestError(err, info.ChannelSetting.Proxy))
	}
	if common2.DebugEnabled && req != nil && req.URL != nil {
		policy := service.NormalizeHTTPTransportPolicy(info.ChannelSetting)
		logger.LogDebug(c, fmt.Sprintf(
			"http transport select: host=%s protocol=%s shards=%d policy=%s",
			req.URL.Host,
			policy.Protocol,
			policy.Shards,
			policy.String(),
		))
	}

	var stopPinger context.CancelFunc
	var pingerDone <-chan struct{}
	if info.IsStream {
		helper.SetEventStreamHeaders(c)
		// 处理流式请求的 ping 保活
		generalSettings := operation_setting.GetGeneralSetting()
		if generalSettings.PingIntervalEnabled && !info.DisablePing {
			pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
			stopPinger, pingerDone = startPingKeepAlive(c, pingInterval)
			// 使用defer确保在任何情况下都能停止ping goroutine
			defer func() {
				if stopPinger != nil {
					stopPinger()
					<-pingerDone
					logger.LogDebug(c, "SSE ping goroutine stopped by defer")
				}
			}()
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		requestURL := ""
		if req.URL != nil {
			requestURL = req.URL.String()
		}
		maskedErr := maskRequestError(err, requestURL)
		logger.LogError(c, "do request failed: "+maskedErr.Error())
		return nil, types.NewError(maskedErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithHideErrMsg("upstream error: do request failed"))
	}
	if resp == nil {
		return nil, errors.New("resp is nil")
	}
	if common2.DebugEnabled {
		policy := service.NormalizeHTTPTransportPolicy(info.ChannelSetting)
		logger.LogDebug(c, fmt.Sprintf(
			"http transport negotiated: host=%s protocol=%s shards=%d policy=%s negotiated=%s",
			req.URL.Host,
			policy.Protocol,
			policy.Shards,
			policy.String(),
			resp.Proto,
		))
	}

	if info.IsStream && resp.Body != nil {
		attempt := info.CurrentUpstreamAttempt()
		resp.Body = newFirstResponseReadCloser(resp.Body, func() {
			info.MarkUpstreamAttemptFirstResponse(attempt)
		})
	}

	if upID := resp.Header.Get(common2.RequestIdKey); upID != "" {
		c.Set(common2.UpstreamRequestIdKey, upID)
	}

	_ = req.Body.Close()
	_ = c.Request.Body.Close()
	return resp, nil
}

func DoTaskApiRequest(a TaskAdaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullRequestURL, err := a.BuildRequestURL(info)
	if err != nil {
		return nil, maskRequestError(err, "")
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", maskRequestError(err, fullRequestURL))
	}
	applyUpstreamContentLength(req, info)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(requestBody), nil
	}

	err = a.BuildRequestHeader(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}
