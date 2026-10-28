package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const quickSettings = `{"chat_template_kwargs":{"enable_thinking":false,"preserve_thinking":false},"temperature":0.7,"top_p":0.8,"top_k":20,"min_p":0.0,"presence_penalty":1.5,"repeat_penalty":1.0,"max_tokens":2048}`
const thoughtfulSettings = `{"chat_template_kwargs":{"enable_thinking":true,"preserve_thinking":false,"reasoning_effort":"medium"},"temperature":1.0,"top_p":0.95,"top_k":20,"min_p":0.0,"presence_penalty":0.0,"repeat_penalty":1.0,"max_tokens":8192}`

type Config struct {
	Listen, PublicURL, Issuer, ClientID, ClientSecret, DataDir, Endpoint, Model string
	ImprintURL, PrivacyURL                                                      string
	Backend, APIKey                                                             string
	Groups                                                                      []string
	Quick, Thoughtful                                                           map[string]any
	GenerationTimeout, SessionTTL                                               time.Duration
	MaxHistory, MaxOutput                                                       int
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func clientSecret() (string, error) {
	return secret("WALTER_OIDC_CLIENT_SECRET")
}

func secret(key string) (string, error) {
	value, path := os.Getenv(key), os.Getenv(key+"_FILE")
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", fmt.Errorf("configure only one %s source", key)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", key, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
func config() (Config, error) {
	c := Config{
		Listen:            env("WALTER_LISTEN", "127.0.0.1:8080"),
		PublicURL:         os.Getenv("WALTER_PUBLIC_URL"),
		Issuer:            os.Getenv("WALTER_OIDC_ISSUER"),
		ClientID:          os.Getenv("WALTER_OIDC_CLIENT_ID"),
		DataDir:           env("WALTER_DATA_DIR", "data"),
		Endpoint:          os.Getenv("WALTER_LLAMA_ENDPOINT"),
		Model:             os.Getenv("WALTER_MODEL"),
		Groups:            strings.Split(env("WALTER_ALLOWED_GROUPS", "walter-users"), ","),
		GenerationTimeout: 2 * time.Hour,
		SessionTTL:        12 * time.Hour,
		MaxHistory:        48000,
		MaxOutput:         4096,
	}
	var err error
	c.Backend = env("WALTER_INFERENCE_BACKEND", "llamacpp")
	if c.Backend != "llamacpp" && c.Backend != "openai" {
		return c, errors.New("inference backend must be llamacpp or openai")
	}
	if endpoint := os.Getenv("WALTER_INFERENCE_ENDPOINT"); endpoint != "" {
		if c.Endpoint != "" {
			return c, errors.New("configure only one inference endpoint")
		}
		c.Endpoint = endpoint
	} else if c.Backend == "openai" {
		return c, errors.New("OpenAI backend requires WALTER_INFERENCE_ENDPOINT")
	}
	c.APIKey, err = secret("WALTER_INFERENCE_API_KEY")
	if err != nil {
		return c, err
	}
	if strings.ContainsAny(c.APIKey, "\r\n") {
		return c, errors.New("invalid inference API key")
	}
	c.ImprintURL, c.PrivacyURL = os.Getenv("WALTER_IMPRINT_URL"), os.Getenv("WALTER_PRIVACY_URL")
	for _, value := range []struct{ key, url string }{{"WALTER_IMPRINT_URL", c.ImprintURL}, {"WALTER_PRIVACY_URL", c.PrivacyURL}} {
		if value.url == "" {
			continue
		}
		u, err := url.Parse(value.url)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return c, fmt.Errorf("%s must be an HTTPS URL", value.key)
		}
	}
	c.ClientSecret, err = clientSecret()
	if err != nil {
		return c, err
	}
	for _, v := range []struct {
		key string
		dst *time.Duration
	}{{"WALTER_GENERATION_TIMEOUT", &c.GenerationTimeout}, {"WALTER_SESSION_TTL", &c.SessionTTL}} {
		if raw := os.Getenv(v.key); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("invalid %s", v.key)
			}
			*v.dst = d
		}
	}
	for _, v := range []struct {
		key string
		dst *int
	}{{"WALTER_MAX_HISTORY_BYTES", &c.MaxHistory}, {"WALTER_MAX_OUTPUT_TOKENS", &c.MaxOutput}} {
		if raw := os.Getenv(v.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return c, fmt.Errorf("invalid %s", v.key)
			}
			*v.dst = n
		}
	}
	quick, thoughtful := quickSettings, thoughtfulSettings
	if c.Backend == "openai" {
		// Portable budgets only: reasoning controls depend on the provider/model.
		quick, thoughtful = `{"max_tokens":2048}`, `{"max_tokens":4096}`
	}
	for _, v := range []struct {
		key, fallback string
		dst           *map[string]any
	}{{"WALTER_QUICK_JSON", quick, &c.Quick}, {"WALTER_THOUGHTFUL_JSON", thoughtful, &c.Thoughtful}} {
		if err := json.Unmarshal([]byte(env(v.key, v.fallback)), v.dst); err != nil || len(*v.dst) == 0 {
			return c, fmt.Errorf("%s must be a nonempty JSON object of verified model settings", v.key)
		}
		if err := validateProfile(c.Backend, *v.dst); err != nil {
			return c, fmt.Errorf("%s: %w", v.key, err)
		}
	}
	for _, raw := range []string{c.PublicURL, c.Issuer} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("public URL and issuer must be HTTPS URLs")
		}
	}
	u, _ := url.Parse(c.PublicURL)
	if u.Path != "" && u.Path != "/" {
		return c, errors.New("public URL must be an origin")
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return c, errors.New("invalid inference endpoint")
	}
	if c.ClientID == "" || c.ClientSecret == "" || c.Model == "" {
		return c, errors.New("OIDC client credentials and model required")
	}
	for i, g := range c.Groups {
		c.Groups[i] = strings.TrimSpace(g)
		if c.Groups[i] == "" {
			return c, errors.New("empty allowed group")
		}
	}
	return c, nil
}

func validateProfile(backend string, profile map[string]any) error {
	for key, value := range profile {
		if err := validateSetting(backend, key, value); err != nil {
			return fmt.Errorf("setting %q: %w", key, err)
		}
	}
	return nil
}

func validateSetting(backend, key string, value any) error {
	if backend == "openai" && (key == "chat_template_kwargs" || key == "top_k" || key == "min_p" || key == "repeat_penalty") {
		return errors.New("requires the llamacpp backend")
	}
	switch key {
	case "reasoning_effort":
		effort, ok := value.(string)
		if !ok || strings.TrimSpace(effort) == "" {
			return errors.New("must be a nonempty string supported by the model")
		}
		// Provider/model vocabularies evolve; do not impose a universal enum.
		return nil
	case "chat_template_kwargs":
		kwargs, ok := value.(map[string]any)
		if !ok || kwargs == nil {
			return errors.New("must be a JSON object")
		}
		return validateTemplateSettings(kwargs)
	case "max_tokens", "top_k", "temperature", "top_p", "min_p", "presence_penalty", "repeat_penalty":
		return validateSamplingNumber(backend, key, value)
	default:
		return errors.New("unsupported model setting")
	}
}

func validateTemplateSettings(kwargs map[string]any) error {
	// Other keys belong to the installed template, not to a universal schema.
	for _, key := range []string{"enable_thinking", "preserve_thinking"} {
		value, exists := kwargs[key]
		if !exists {
			continue
		}
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", key)
		}
	}
	if value, exists := kwargs["reasoning_effort"]; exists {
		return validateSetting("llamacpp", "reasoning_effort", value)
	}
	return nil
}

func validateSamplingNumber(backend, key string, value any) error {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return errors.New("must be a finite number")
	}
	switch key {
	case "max_tokens":
		if n <= 0 || n != math.Trunc(n) || n > math.MaxInt32 {
			return errors.New("must be a positive 32-bit integer")
		}
	case "top_k":
		// llama.cpp permits nonpositive integers to disable top-k sampling.
		if n != math.Trunc(n) || n < math.MinInt32 || n > math.MaxInt32 {
			return errors.New("must be a 32-bit integer")
		}
	case "top_p", "min_p":
		if n < 0 || n > 1 {
			return errors.New("must be between 0 and 1")
		}
	case "temperature":
		if backend == "openai" && (n < 0 || n > 2) {
			return errors.New("must be between 0 and 2 for the openai backend")
		}
	case "presence_penalty":
		if backend == "openai" && (n < -2 || n > 2) {
			return errors.New("must be between -2 and 2 for the openai backend")
		}
	}
	return nil
}
