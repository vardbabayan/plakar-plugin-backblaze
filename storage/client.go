package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type config struct {
	endpoint  string // host only, e.g. "s3.us-west-004.backblazeb2.com"
	region    string // e.g. "us-west-004"
	bucket    string
	prefixDir string
	accessKey string
	secretKey string
	useTLS    bool
	insecure  bool
	pathStyle bool
}

func boolOpt(cfg map[string]string, key string, def bool) (bool, error) {
	value, ok := cfg[key]
	if !ok {
		return def, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s value", key)
	}
	return parsed, nil
}

func regionFromEndpoint(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[0] == "s3" {
		return parts[1]
	}
	return ""
}

func parseConfig(cfg map[string]string) (*config, error) {
	c := &config{}

	var ok bool
	if c.accessKey, ok = cfg["access_key"]; !ok {
		return nil, fmt.Errorf("missing access_key")
	}
	if c.secretKey, ok = cfg["secret_access_key"]; !ok {
		return nil, fmt.Errorf("missing secret_access_key")
	}

	var err error
	if c.useTLS, err = boolOpt(cfg, "use_tls", true); err != nil {
		return nil, err
	}
	if c.insecure, err = boolOpt(cfg, "tls_insecure_no_verify", false); err != nil {
		return nil, err
	}
	if c.pathStyle, err = boolOpt(cfg, "use_path_style", false); err != nil {
		return nil, err
	}

	u, err := url.Parse(cfg["location"])
	if err != nil {
		return nil, fmt.Errorf("parse location: %w", err)
	}

	if endpoint := cfg["endpoint"]; endpoint != "" && u.Host != endpoint {
		u.Path = "/" + strings.TrimPrefix(u.Host+u.Path, "/")
		u.Host = endpoint
	}
	c.endpoint = u.Host

	source := u.Path
	if root := cfg["root"]; root != "" {
		source = root
	}
	c.bucket, c.prefixDir, _ = strings.Cut(strings.TrimPrefix(source, "/"), "/")

	if c.bucket == "" || c.endpoint == "" {
		return nil, fmt.Errorf("failed to parse the location: bucket name or endpoint are empty")
	}

	if !strings.HasPrefix(c.prefixDir, "/") {
		c.prefixDir = "/" + c.prefixDir
	}
	if !strings.HasSuffix(c.prefixDir, "/") {
		c.prefixDir += "/"
	}

	c.region = cfg["region"]
	if c.region == "" {
		c.region = regionFromEndpoint(c.endpoint)
	}
	if c.region == "" {
		return nil, fmt.Errorf("cannot derive region from endpoint %q, set region explicitly", c.endpoint)
	}

	return c, nil
}

type dropChecksumHeaders struct{}

func (dropChecksumHeaders) ID() string { return "B2DropChecksumHeaders" }

func (dropChecksumHeaders) HandleFinalize(
	ctx context.Context,
	in middleware.FinalizeInput,
	next middleware.FinalizeHandler,
) (middleware.FinalizeOutput, middleware.Metadata, error) {
	if req, ok := in.Request.(*smithyhttp.Request); ok {
		for name := range req.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-amz-checksum-") || lower == "x-amz-sdk-checksum-algorithm" {
				req.Header.Del(name)
			}
		}
	}
	return next.HandleFinalize(ctx, in)
}

func newClient(c *config) *s3.Client {
	scheme := "https://"
	if !c.useTLS {
		scheme = "http://"
	}

	httpClient := http.DefaultClient
	if c.useTLS && c.insecure {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		httpClient = &http.Client{Transport: transport}
	}

	return s3.New(s3.Options{
		Region:       c.region,
		BaseEndpoint: aws.String(scheme + c.endpoint),
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(c.accessKey, c.secretKey, ""),
		),
		HTTPClient:   httpClient,
		UsePathStyle: c.pathStyle,

		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,

		RetryMaxAttempts: 5,

		APIOptions: []func(*middleware.Stack) error{
			func(stack *middleware.Stack) error {
				return stack.Finalize.Insert(dropChecksumHeaders{}, "Signing", middleware.Before)
			},
		},
	})
}
