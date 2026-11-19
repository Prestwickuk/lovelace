package backup

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/secrets"
)

const (
	cloudRequestTimeout   = 45 * time.Second
	maxCloudResponseBytes = 4 << 20
	azureAPIVersion       = "2021-08-06"
)

// CloudClientConfig configures outbound cloud storage transport & test overrides.
type CloudClientConfig struct {
	AllowLoopbackHTTPForTests bool
	GCSTokenURLOverride       string
	Clock                     func() time.Time
}

type RemoteObject struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
}

// ValidateAndExtractCredentialHint validates provider-specific credentials and returns a normalized credential map + masked hint.
func ValidateAndExtractCredentialHint(
	provider domain.BackupProvider,
	encryptArchive bool,
	in map[string]string,
	existing map[string]string,
) (map[string]string, string, error) {
	merged := make(map[string]string)
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range in {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			merged[k] = trimmed
		}
	}

	errs := make(map[string]string)
	if encryptArchive {
		pp := strings.TrimSpace(merged["archive_passphrase"])
		if len(pp) < MinArchivePassphraseLength {
			errs["archive_passphrase"] = fmt.Sprintf("passphrase of at least %d characters is required when archive encryption is enabled", MinArchivePassphraseLength)
		}
	}

	var hint string
	switch provider {
	case domain.BackupProviderAWSS3, domain.BackupProviderS3Compatible:
		ak := strings.TrimSpace(merged["access_key_id"])
		sk := strings.TrimSpace(merged["secret_access_key"])
		if ak == "" {
			errs["access_key_id"] = "access key ID is required"
		}
		if sk == "" {
			errs["secret_access_key"] = "secret access key is required"
		}
		hint = secrets.MaskSecret(ak)

	case domain.BackupProviderGCS:
		saRaw := strings.TrimSpace(merged["service_account_json"])
		if saRaw == "" {
			errs["service_account_json"] = "GCP service account JSON key is required"
		} else {
			email, _, perr := parseGCPServiceAccount(saRaw)
			if perr != nil {
				errs["service_account_json"] = perr.Error()
			} else {
				hint = maskEmailHint(email)
			}
		}

	case domain.BackupProviderAzureBlob:
		acct := strings.TrimSpace(merged["account_name"])
		keyB64 := strings.TrimSpace(merged["account_key"])
		sas := strings.TrimSpace(merged["sas_token"])
		if acct == "" {
			errs["account_name"] = "Azure storage account name is required"
		}
		if keyB64 == "" && sas == "" {
			errs["account_key"] = "provide either an Azure storage account key or a SAS token"
		}
		if keyB64 != "" {
			if _, derr := base64.StdEncoding.DecodeString(keyB64); derr != nil {
				errs["account_key"] = "Azure account key must be valid Base64"
			}
		}
		hint = acct + " (" + secrets.MaskSecret(keyB64+sas) + ")"

	default:
		errs["provider"] = "unsupported cloud backup provider"
	}

	if len(errs) > 0 {
		return nil, "", domain.NewValidationError(errs)
	}
	return merged, hint, nil
}

func maskEmailHint(email string) string {
	parts := strings.SplitN(strings.TrimSpace(email), "@", 2)
	if len(parts) != 2 {
		return secrets.MaskSecret(email)
	}
	local := parts[0]
	if len(local) > 4 {
		local = local[:4] + "••••"
	}
	return local + "@" + parts[1]
}

type CloudStorageClient struct {
	httpClient *http.Client
	cfg        CloudClientConfig
}

func NewCloudStorageClient(cfg CloudClientConfig) *CloudStorageClient {
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	rawDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	dialCtx := rawDialer.DialContext
	if !cfg.AllowLoopbackHTTPForTests {
		dialCtx = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				if err != nil {
					return nil, err
				}
				return nil, errors.New("no IP addresses resolved for cloud storage endpoint")
			}
			for _, ip := range ips {
				if isBlockedCloudIP(ip) {
					return nil, fmt.Errorf("blocked non-public IP address for cloud storage endpoint")
				}
			}
			var lastErr error
			for _, ip := range ips {
				conn, dErr := rawDialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
				if dErr == nil {
					return conn, nil
				}
				lastErr = dErr
			}
			return nil, lastErr
		}
	}

	transport := &http.Transport{
		DialContext:           dialCtx,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	return &CloudStorageClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   cloudRequestTimeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func isBlockedCloudIP(ip netip.Addr) bool {
	u := ip.Unmap()
	if !u.IsValid() || u.IsLoopback() || u.IsPrivate() || u.IsLinkLocalUnicast() || u.IsLinkLocalMulticast() || u.IsMulticast() || u.IsUnspecified() {
		return true
	}
	// Block CGNAT 100.64.0.0/10 and AWS/GCP/Azure metadata IP 169.254.169.254 explicitly.
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	if cgnat.Contains(u) {
		return true
	}
	return false
}

// VerifyTarget probes read/list access against the configured bucket or container without writing objects.
func (c *CloudStorageClient) VerifyTarget(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string) error {
	if err := target.Validate(c.cfg.AllowLoopbackHTTPForTests); err != nil {
		return err
	}
	_, err := c.listObjects(ctx, target, creds, 1)
	return err
}

// UploadAndPrune uploads objectKey to target and prunes older backups exceeding target.RetentionCount.
func (c *CloudStorageClient) UploadAndPrune(
	ctx context.Context,
	target domain.BackupStorageTarget,
	creds map[string]string,
	fileName string,
	payload []byte,
) (string, int, error) {
	if err := target.Validate(c.cfg.AllowLoopbackHTTPForTests); err != nil {
		return "", 0, err
	}
	objectKey := domain.NormalizeBackupPrefix(target.KeyPrefix) + strings.TrimLeft(fileName, "/")
	if err := c.putObject(ctx, target, creds, objectKey, payload); err != nil {
		return "", 0, err
	}

	pruned := 0
	if target.RetentionCount > 0 {
		objs, lerr := c.listObjects(ctx, target, creds, 500)
		if lerr == nil && len(objs) > target.RetentionCount {
			var candidates []RemoteObject
			prefix := domain.NormalizeBackupPrefix(target.KeyPrefix) + "lovelace-backup-"
			for _, o := range objs {
				if strings.HasPrefix(o.Key, prefix) {
					candidates = append(candidates, o)
				}
			}
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].Key > candidates[j].Key
			})
			if len(candidates) > target.RetentionCount {
				for _, old := range candidates[target.RetentionCount:] {
					if derr := c.deleteObject(ctx, target, creds, old.Key); derr == nil {
						pruned++
					}
				}
			}
		}
	}
	return objectKey, pruned, nil
}

func (c *CloudStorageClient) putObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string, payload []byte) error {
	switch target.Provider {
	case domain.BackupProviderAWSS3, domain.BackupProviderS3Compatible:
		return c.s3PutObject(ctx, target, creds, objectKey, payload)
	case domain.BackupProviderGCS:
		return c.gcsPutObject(ctx, target, creds, objectKey, payload)
	case domain.BackupProviderAzureBlob:
		return c.azurePutObject(ctx, target, creds, objectKey, payload)
	default:
		return fmt.Errorf("unsupported cloud provider %s", target.Provider)
	}
}

func (c *CloudStorageClient) listObjects(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, maxKeys int) ([]RemoteObject, error) {
	switch target.Provider {
	case domain.BackupProviderAWSS3, domain.BackupProviderS3Compatible:
		return c.s3ListObjects(ctx, target, creds, maxKeys)
	case domain.BackupProviderGCS:
		return c.gcsListObjects(ctx, target, creds, maxKeys)
	case domain.BackupProviderAzureBlob:
		return c.azureListObjects(ctx, target, creds, maxKeys)
	default:
		return nil, fmt.Errorf("unsupported cloud provider %s", target.Provider)
	}
}

func (c *CloudStorageClient) deleteObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string) error {
	switch target.Provider {
	case domain.BackupProviderAWSS3, domain.BackupProviderS3Compatible:
		return c.s3DeleteObject(ctx, target, creds, objectKey)
	case domain.BackupProviderGCS:
		return c.gcsDeleteObject(ctx, target, creds, objectKey)
	case domain.BackupProviderAzureBlob:
		return c.azureDeleteObject(ctx, target, creds, objectKey)
	default:
		return fmt.Errorf("unsupported cloud provider %s", target.Provider)
	}
}

// --- AWS S3 & S3-Compatible (SigV4) ---

func resolveS3BaseAndPathPrefix(target domain.BackupStorageTarget) (*url.URL, string, string, error) {
	region := strings.TrimSpace(target.Region)
	if region == "" {
		region = "auto"
	}
	if strings.TrimSpace(target.EndpointURL) != "" {
		u, err := url.Parse(strings.TrimRight(strings.TrimSpace(target.EndpointURL), "/"))
		if err != nil {
			return nil, "", "", err
		}
		basePath := strings.TrimRight(u.Path, "/") + "/" + target.BucketOrContainer
		u.Path = ""
		return u, basePath, region, nil
	}
	if !strings.Contains(target.BucketOrContainer, ".") {
		u, err := url.Parse(fmt.Sprintf("https://%s.s3.%s.amazonaws.com", target.BucketOrContainer, region))
		return u, "", region, err
	}
	u, err := url.Parse(fmt.Sprintf("https://s3.%s.amazonaws.com", region))
	return u, "/" + target.BucketOrContainer, region, err
}

func uriEncodeS3(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '~' || ch == '.' {
			b.WriteByte(ch)
		} else if ch == '/' && !encodeSlash {
			b.WriteByte('/')
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func (c *CloudStorageClient) s3SignedRequest(
	ctx context.Context,
	method string,
	target domain.BackupStorageTarget,
	creds map[string]string,
	objectKey string,
	query url.Values,
	body []byte,
	contentType string,
) (*http.Response, []byte, error) {
	baseURL, bucketPrefix, region, err := resolveS3BaseAndPathPrefix(target)
	if err != nil {
		return nil, nil, err
	}
	rawPath := bucketPrefix
	if objectKey != "" {
		rawPath += "/" + strings.TrimLeft(objectKey, "/")
	}
	if rawPath == "" {
		rawPath = "/"
	}
	canonicalURI := uriEncodeS3(rawPath, false)

	var queryParts []string
	if len(query) > 0 {
		keys := make([]string, 0, len(query))
		for k := range query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals := append([]string(nil), query[k]...)
			sort.Strings(vals)
			for _, v := range vals {
				queryParts = append(queryParts, uriEncodeS3(k, true)+"="+uriEncodeS3(v, true))
			}
		}
	}
	canonicalQuery := strings.Join(queryParts, "&")

	reqURL := baseURL.Scheme + "://" + baseURL.Host + canonicalURI
	if canonicalQuery != "" {
		reqURL += "?" + canonicalQuery
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	now := c.cfg.Clock().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadSum := sha256.Sum256(body)
	payloadHex := hex.EncodeToString(payloadSum[:])

	req.Header.Set("Host", baseURL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHex)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", strings.ToLower(baseURL.Host), payloadHex, amzDate)
	if contentType != "" {
		signedHeaders = "content-type;" + signedHeaders
		canonicalHeaders = fmt.Sprintf("content-type:%s\n%s", contentType, canonicalHeaders)
	}

	canonicalReq := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHex,
	}, "\n")
	crSum := sha256.Sum256([]byte(canonicalReq))
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(crSum[:]),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+creds["secret_access_key"]), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds["access_key_id"], scope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("S3 request failed: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxCloudResponseBytes))
	return resp, respBytes, nil
}

func (c *CloudStorageClient) s3PutObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string, payload []byte) error {
	resp, _, err := c.s3SignedRequest(ctx, http.MethodPut, target, creds, objectKey, nil, payload, "application/octet-stream")
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("S3 upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}

type s3ListBucketResult struct {
	Contents []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
}

func (c *CloudStorageClient) s3ListObjects(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, maxKeys int) ([]RemoteObject, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("prefix", domain.NormalizeBackupPrefix(target.KeyPrefix))
	if maxKeys > 0 {
		q.Set("max-keys", strconv.Itoa(maxKeys))
	}
	resp, body, err := c.s3SignedRequest(ctx, http.MethodGet, target, creds, "", q, nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("S3 bucket check returned HTTP %d", resp.StatusCode)
	}
	var parsed s3ListBucketResult
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode S3 list response: %w", err)
	}
	out := make([]RemoteObject, 0, len(parsed.Contents))
	for _, item := range parsed.Contents {
		lm, _ := time.Parse(time.RFC3339, strings.TrimSpace(item.LastModified))
		out = append(out, RemoteObject{
			Key:          item.Key,
			SizeBytes:    item.Size,
			LastModified: lm,
		})
	}
	return out, nil
}

func (c *CloudStorageClient) s3DeleteObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string) error {
	resp, _, err := c.s3SignedRequest(ctx, http.MethodDelete, target, creds, objectKey, nil, nil, "")
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("S3 delete returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- Google Cloud Storage (OAuth2 Service Account JWT + JSON API) ---

type gcpServiceAccountDoc struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

func parseGCPServiceAccount(raw string) (string, *rsa.PrivateKey, error) {
	var doc gcpServiceAccountDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", nil, errors.New("must be valid GCP service account JSON")
	}
	if strings.TrimSpace(doc.ClientEmail) == "" || strings.TrimSpace(doc.PrivateKey) == "" {
		return "", nil, errors.New("service account JSON must include client_email and private_key")
	}
	block, _ := pem.Decode([]byte(doc.PrivateKey))
	if block == nil {
		return "", nil, errors.New("service account private_key is not valid PEM")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if pkcs1Key, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			return strings.TrimSpace(doc.ClientEmail), pkcs1Key, nil
		}
		return "", nil, errors.New("service account private_key must be an RSA key")
	}
	rsaKey, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return "", nil, errors.New("service account private_key must be an RSA key")
	}
	return strings.TrimSpace(doc.ClientEmail), rsaKey, nil
}

func (c *CloudStorageClient) gcsAccessToken(ctx context.Context, creds map[string]string) (string, error) {
	email, rsaKey, err := parseGCPServiceAccount(creds["service_account_json"])
	if err != nil {
		return "", err
	}
	tokenURL := "https://oauth2.googleapis.com/token"
	if c.cfg.AllowLoopbackHTTPForTests && strings.TrimSpace(c.cfg.GCSTokenURLOverride) != "" {
		tokenURL = strings.TrimSpace(c.cfg.GCSTokenURLOverride)
	}
	now := c.cfg.Clock().UTC()
	hdrJSON := `{"alg":"RS256","typ":"JWT"}`
	claimsBytes, _ := json.Marshal(map[string]any{
		"iss":   email,
		"scope": "https://www.googleapis.com/auth/devstorage.read_write",
		"aud":   tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(55 * time.Minute).Unix(),
	})
	signingInput := base64.RawURLEncoding.EncodeToString([]byte(hdrJSON)) + "." + base64.RawURLEncoding.EncodeToString(claimsBytes)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign GCS JWT: %w", err)
	}
	jwt := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", jwt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GCS OAuth token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCloudResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GCS OAuth token exchange returned HTTP %d", resp.StatusCode)
	}
	var tokResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokResp); err != nil || strings.TrimSpace(tokResp.AccessToken) == "" {
		return "", errors.New("invalid GCS OAuth token response")
	}
	return tokResp.AccessToken, nil
}

func resolveGCSBaseURL(target domain.BackupStorageTarget) string {
	if strings.TrimSpace(target.EndpointURL) != "" {
		return strings.TrimRight(strings.TrimSpace(target.EndpointURL), "/")
	}
	return "https://storage.googleapis.com"
}

func (c *CloudStorageClient) gcsPutObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string, payload []byte) error {
	tok, err := c.gcsAccessToken(ctx, creds)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		resolveGCSBaseURL(target),
		url.PathEscape(target.BucketOrContainer),
		url.QueryEscape(objectKey),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(payload))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GCS upload failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxCloudResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GCS upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *CloudStorageClient) gcsListObjects(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, maxKeys int) ([]RemoteObject, error) {
	tok, err := c.gcsAccessToken(ctx, creds)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("prefix", domain.NormalizeBackupPrefix(target.KeyPrefix))
	if maxKeys > 0 {
		q.Set("maxResults", strconv.Itoa(maxKeys))
	}
	u := fmt.Sprintf("%s/storage/v1/b/%s/o?%s",
		resolveGCSBaseURL(target),
		url.PathEscape(target.BucketOrContainer),
		q.Encode(),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GCS bucket check failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCloudResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GCS bucket check returned HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Items []struct {
			Name    string `json:"name"`
			Size    string `json:"size"`
			Updated string `json:"updated"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode GCS list response: %w", err)
	}
	out := make([]RemoteObject, 0, len(doc.Items))
	for _, it := range doc.Items {
		sz, _ := strconv.ParseInt(it.Size, 10, 64)
		lm, _ := time.Parse(time.RFC3339, it.Updated)
		out = append(out, RemoteObject{
			Key:          it.Name,
			SizeBytes:    sz,
			LastModified: lm,
		})
	}
	return out, nil
}

func (c *CloudStorageClient) gcsDeleteObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string) error {
	tok, err := c.gcsAccessToken(ctx, creds)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s",
		resolveGCSBaseURL(target),
		url.PathEscape(target.BucketOrContainer),
		url.PathEscape(objectKey),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxCloudResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GCS delete returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- Microsoft Azure Blob Storage (SharedKey HMAC-SHA256 & SAS) ---

func resolveAzureBaseURL(target domain.BackupStorageTarget, account string) string {
	if strings.TrimSpace(target.EndpointURL) != "" {
		return strings.TrimRight(strings.TrimSpace(target.EndpointURL), "/")
	}
	return fmt.Sprintf("https://%s.blob.core.windows.net", account)
}

func (c *CloudStorageClient) azureRequest(
	ctx context.Context,
	method string,
	target domain.BackupStorageTarget,
	creds map[string]string,
	objectKey string,
	query url.Values,
	body []byte,
	extraMSHeaders map[string]string,
) (*http.Response, []byte, error) {
	account := strings.TrimSpace(creds["account_name"])
	base := resolveAzureBaseURL(target, account)
	u, err := url.Parse(base)
	if err != nil {
		return nil, nil, err
	}

	resourcePath := strings.TrimRight(u.Path, "/") + "/" + url.PathEscape(target.BucketOrContainer)
	if objectKey != "" {
		for _, seg := range strings.Split(strings.TrimLeft(objectKey, "/"), "/") {
			resourcePath += "/" + url.PathEscape(seg)
		}
	}

	reqQuery := url.Values{}
	for k, vals := range query {
		for _, v := range vals {
			reqQuery.Add(k, v)
		}
	}

	sasToken := strings.TrimLeft(strings.TrimSpace(creds["sas_token"]), "?")
	if sasToken != "" && strings.TrimSpace(creds["account_key"]) == "" {
		if sasVals, serr := url.ParseQuery(sasToken); serr == nil {
			for k, vals := range sasVals {
				for _, v := range vals {
					reqQuery.Add(k, v)
				}
			}
		}
	}

	fullURL := u.Scheme + "://" + u.Host + resourcePath
	if len(reqQuery) > 0 {
		fullURL += "?" + reqQuery.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	msDate := c.cfg.Clock().UTC().Format(http.TimeFormat)
	req.Header.Set("x-ms-date", msDate)
	req.Header.Set("x-ms-version", azureAPIVersion)
	for k, v := range extraMSHeaders {
		req.Header.Set(k, v)
	}

	var contentLenStr string
	contentType := ""
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
		contentLenStr = strconv.Itoa(len(body))
		contentType = "application/octet-stream"
		req.Header.Set("Content-Type", contentType)
	}

	if acctKeyB64 := strings.TrimSpace(creds["account_key"]); acctKeyB64 != "" {
		rawKey, err := base64.StdEncoding.DecodeString(acctKeyB64)
		if err != nil {
			return nil, nil, errors.New("invalid Azure account_key base64")
		}
		var msKeys []string
		msMap := map[string]string{
			"x-ms-date":    msDate,
			"x-ms-version": azureAPIVersion,
		}
		for k, v := range extraMSHeaders {
			msMap[strings.ToLower(k)] = strings.TrimSpace(v)
		}
		for k := range msMap {
			msKeys = append(msKeys, k)
		}
		sort.Strings(msKeys)
		var canonHeaders strings.Builder
		for _, k := range msKeys {
			canonHeaders.WriteString(k)
			canonHeaders.WriteByte(':')
			canonHeaders.WriteString(msMap[k])
			canonHeaders.WriteByte('\n')
		}

		var canonResource strings.Builder
		canonResource.WriteByte('/')
		canonResource.WriteString(account)
		canonResource.WriteString(resourcePath)
		if len(query) > 0 {
			var qKeys []string
			lowerQuery := make(map[string][]string, len(query))
			for k, vals := range query {
				lk := strings.ToLower(k)
				lowerQuery[lk] = append([]string(nil), vals...)
				qKeys = append(qKeys, lk)
			}
			sort.Strings(qKeys)
			for _, lk := range qKeys {
				vals := lowerQuery[lk]
				sort.Strings(vals)
				canonResource.WriteByte('\n')
				canonResource.WriteString(lk)
				canonResource.WriteByte(':')
				canonResource.WriteString(strings.Join(vals, ","))
			}
		}

		stringToSign := strings.Join([]string{
			method,
			"",            // Content-Encoding
			"",            // Content-Language
			contentLenStr, // Content-Length (empty when 0)
			"",            // Content-MD5
			contentType,   // Content-Type
			"",            // Date
			"",            // If-Modified-Since
			"",            // If-Match
			"",            // If-None-Match
			"",            // If-Unmodified-Since
			"",            // Range
			canonHeaders.String() + canonResource.String(),
		}, "\n")

		mac := hmac.New(sha256.New, rawKey)
		_, _ = mac.Write([]byte(stringToSign))
		sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		req.Header.Set("Authorization", fmt.Sprintf("SharedKey %s:%s", account, sig))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("Azure Blob request failed: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxCloudResponseBytes))
	return resp, respBytes, nil
}

func (c *CloudStorageClient) azurePutObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string, payload []byte) error {
	resp, _, err := c.azureRequest(ctx, http.MethodPut, target, creds, objectKey, nil, payload, map[string]string{
		"x-ms-blob-type": "BlockBlob",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Azure Blob upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}

type azureListBlobsResult struct {
	Blobs struct {
		Blob []struct {
			Name       string `xml:"Name"`
			Properties struct {
				ContentLength int64  `xml:"Content-Length"`
				LastModified  string `xml:"Last-Modified"`
			} `xml:"Properties"`
		} `xml:"Blob"`
	} `xml:"Blobs"`
}

func (c *CloudStorageClient) azureListObjects(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, maxKeys int) ([]RemoteObject, error) {
	q := url.Values{}
	q.Set("restype", "container")
	q.Set("comp", "list")
	q.Set("prefix", domain.NormalizeBackupPrefix(target.KeyPrefix))
	if maxKeys > 0 {
		q.Set("maxresults", strconv.Itoa(maxKeys))
	}
	resp, body, err := c.azureRequest(ctx, http.MethodGet, target, creds, "", q, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Azure container check returned HTTP %d", resp.StatusCode)
	}
	var parsed azureListBlobsResult
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode Azure list response: %w", err)
	}
	out := make([]RemoteObject, 0, len(parsed.Blobs.Blob))
	for _, b := range parsed.Blobs.Blob {
		lm, _ := time.Parse(http.TimeFormat, strings.TrimSpace(b.Properties.LastModified))
		out = append(out, RemoteObject{
			Key:          b.Name,
			SizeBytes:    b.Properties.ContentLength,
			LastModified: lm,
		})
	}
	return out, nil
}

func (c *CloudStorageClient) azureDeleteObject(ctx context.Context, target domain.BackupStorageTarget, creds map[string]string, objectKey string) error {
	resp, _, err := c.azureRequest(ctx, http.MethodDelete, target, creds, objectKey, nil, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Azure Blob delete returned HTTP %d", resp.StatusCode)
	}
	return nil
}
