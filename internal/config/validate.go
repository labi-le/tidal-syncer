package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	minConcurrency = 1
	maxConcurrency = 8
	minInterval    = time.Minute

	placeholderTitle = "title"
	placeholderExt   = "ext"
)

// Validate reports the first field whose value falls outside its allowed domain.
func (c Config) Validate() error {
	if err := requireOneOf("removal.policy", c.Removal.Policy,
		[]string{removalKeep, removalMirror, removalTrash}); err != nil {
		return err
	}
	if err := validateConcurrency(c.Concurrency); err != nil {
		return err
	}
	if err := validateInterval(c.Daemon.Interval); err != nil {
		return err
	}
	if err := requireOneOf("quality.floor", string(c.Quality.Floor),
		[]string{string(tidal.QualityLossless), string(tidal.QualityHiResLossless)}); err != nil {
		return err
	}
	if err := validatePathTemplate(c.PathTemplate); err != nil {
		return err
	}
	if err := requireNonEmpty("tidal_auth.client_id", c.TidalAuth.ClientID); err != nil {
		return err
	}
	if err := requireNonEmpty("tidal_auth.client_secret", c.TidalAuth.ClientSecret); err != nil {
		return err
	}
	return nil
}

func requireOneOf(field, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s %q is invalid: must be one of %v", field, value, allowed)
}

func requireNonEmpty(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func validateConcurrency(n int) error {
	if n < minConcurrency || n > maxConcurrency {
		return fmt.Errorf("concurrency %d is out of range: must be between %d and %d",
			n, minConcurrency, maxConcurrency)
	}
	return nil
}

func validateInterval(d time.Duration) error {
	if d < minInterval {
		return fmt.Errorf("daemon.interval %s is too small: must be at least %s", d, minInterval)
	}
	return nil
}

func validatePathTemplate(tmpl string) error {
	placeholders, err := parseTemplate(tmpl)
	if err != nil {
		return fmt.Errorf("path_template is invalid: %w", err)
	}
	for _, required := range []string{placeholderTitle, placeholderExt} {
		if !slices.Contains(placeholders, required) {
			return fmt.Errorf("path_template is missing required placeholder {%s}", required)
		}
	}
	return nil
}

func parseTemplate(tmpl string) ([]string, error) {
	var placeholders []string
	var name strings.Builder
	inPlaceholder := false
	for i, r := range tmpl {
		switch r {
		case '{':
			if inPlaceholder {
				return nil, fmt.Errorf("nested '{' at position %d", i)
			}
			inPlaceholder = true
			name.Reset()
		case '}':
			if !inPlaceholder {
				return nil, fmt.Errorf("unmatched '}' at position %d", i)
			}
			placeholder := name.String()
			if placeholder == "" {
				return nil, fmt.Errorf("empty placeholder at position %d", i)
			}
			placeholders = append(placeholders, placeholder)
			inPlaceholder = false
		default:
			if inPlaceholder {
				name.WriteRune(r)
			}
		}
	}
	if inPlaceholder {
		return nil, errors.New("unterminated placeholder: missing closing brace")
	}
	return placeholders, nil
}
