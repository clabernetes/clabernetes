//nolint:err113,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package deviceplan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// PayloadFetcher materializes digest-pinned URL inputs before imported hooks execute. The
// separate worker is the only planning phase with egress; SealNetwork removes its Pod network
// before the locked-down planner container starts.
type PayloadFetcher struct {
	HTTPClient  *http.Client
	SealNetwork func() error
}

// FetchURLPayloads downloads all URL payloads into the same opaque projection layout used by
// ConfigMaps and Secrets, verifies every digest, and then seals the shared Pod network namespace.
func (f PayloadFetcher) FetchURLPayloads(
	ctx context.Context,
	input Input,
	payloadRoot string,
) error {
	if ctx == nil {
		return planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}

	normalized, err := NormalizeInput(input)
	if err != nil {
		return err
	}

	payloadRoot = filepath.Clean(payloadRoot)
	if !filepath.IsAbs(payloadRoot) || payloadRoot == string(filepath.Separator) {
		return planningError(
			ErrorInvalidInput,
			"payloadFetcher.root",
			"payload projection root must be a scoped absolute path",
			nil,
		)
	}

	if err = os.MkdirAll(payloadRoot, 0o700); err != nil {
		return planningError(
			ErrorSideEffect,
			"payloadFetcher.root",
			"cannot create payload root",
			err,
		)
	}

	client := f.HTTPClient
	if client == nil {
		client = securePayloadHTTPClient()
	}

	contentByIdentity := map[string][]byte{}
	found := false

	for _, payload := range normalized.Payloads {
		if payload.Kind != PayloadURL {
			continue
		}

		found = true
		identity := payload.Reference + "\x00" + payload.Digest

		content, exists := contentByIdentity[identity]
		if !exists {
			content, err = readURLPayload(ctx, client, payload.Reference)
			if err != nil {
				return withNodeID(err, payload.NodeID)
			}

			if Digest(content) != payload.Digest {
				return &Error{
					Code: ErrorInvariant, NodeID: payload.NodeID, Field: "payloadFetcher.digest",
					Behavior: behaviorURLPayload,
					Message:  "URL payload bytes differ from the accepted digest",
				}
			}

			contentByIdentity[identity] = content
		}

		if err = stageArtifactContent(
			content,
			payloadRoot,
			payload.ID,
			"source",
			0o444,
			nil,
			nil,
			nil,
			behaviorURLPayload,
		); err != nil {
			return withNodeID(err, payload.NodeID)
		}

		if err = os.Chmod(filepath.Join(payloadRoot, ArtifactNodeDirectory(payload.ID)), 0o755); err != nil { //nolint:gosec // the mode matches the staged artifact contract.
			return planningError(
				ErrorSideEffect,
				"payloadFetcher.root",
				"cannot finalize payload directory permissions",
				err,
			)
		}
	}

	if !found {
		return planningError(
			ErrorMissingInput,
			"payloadFetcher.payloads",
			"input contains no URL payload",
			nil,
		)
	}

	if err = os.Chmod(payloadRoot, 0o755); err != nil { //nolint:gosec // the mode matches the staged artifact contract.
		return planningError(
			ErrorSideEffect,
			"payloadFetcher.root",
			"cannot finalize payload root permissions",
			err,
		)
	}

	seal := f.SealNetwork
	if seal == nil {
		seal = sealPlannerNetwork
	}

	if err = seal(); err != nil {
		return planningError(
			ErrorSideEffect,
			"payloadFetcher.network",
			"cannot seal planner network namespace",
			err,
		)
	}

	return nil
}

func readURLPayload(ctx context.Context, client *http.Client, reference string) ([]byte, error) {
	parsed, err := url.ParseRequestURI(reference)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, &Error{
			Code: ErrorInvalidInput, Field: fieldPayloadsURL, Behavior: behaviorURLPayload,
			Message: "URL payload reference is not an absolute credential-free HTTP(S) URL",
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, reference, http.NoBody)
	if err != nil {
		return nil, planningError(
			ErrorInvalidInput,
			fieldPayloadsURL,
			"cannot create URL request",
			err,
		)
	}

	request.Header.Set("Accept-Encoding", "identity")

	response, err := client.Do(request)
	if err != nil {
		return nil, planningError(ErrorSideEffect, fieldPayloadsURL, "URL request failed", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &Error{
			Code: ErrorSideEffect, Field: fieldPayloadsURL, Behavior: behaviorURLPayload,
			Message: fmt.Sprintf("URL payload returned HTTP status %d", response.StatusCode),
		}
	}

	if response.ContentLength > maxPreparedPayloadBytes {
		return nil, &Error{
			Code: ErrorUnsupported, Field: fieldPayloadsURL, Behavior: behaviorURLPayload,
			Message: "URL payload exceeds the size limit",
		}
	}

	content, err := io.ReadAll(io.LimitReader(response.Body, maxPreparedPayloadBytes+1))
	if err != nil {
		return nil, planningError(
			ErrorSideEffect,
			fieldPayloadsURL,
			"cannot read URL response",
			err,
		)
	}

	if len(content) > maxPreparedPayloadBytes {
		return nil, &Error{
			Code: ErrorUnsupported, Field: fieldPayloadsURL, Behavior: behaviorURLPayload,
			Message: "URL payload exceeds the size limit",
		}
	}

	return content, nil
}

func securePayloadHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid URL endpoint")
			}

			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("cannot resolve URL endpoint")
			}

			for _, candidate := range addresses {
				if !publicPayloadAddress(candidate) {
					return nil, errors.New("URL endpoint resolves to a non-public address")
				}
			}

			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 || request.URL.User != nil ||
				(request.URL.Scheme != "http" && request.URL.Scheme != "https") {
				return errors.New("URL redirect is outside the allowed HTTP(S) boundary")
			}

			return nil
		},
	}
}

func publicPayloadAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}

	for _, rawPrefix := range []string{
		"100.64.0.0/10",
		"192.0.0.0/24",
		"198.18.0.0/15",
		"2001:db8::/32",
	} {
		prefix := netip.MustParsePrefix(rawPrefix)
		if prefix.Contains(address) {
			return false
		}
	}

	return true
}
