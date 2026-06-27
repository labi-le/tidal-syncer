// Package ctxlog binds operation-scoped fields to a zerolog logger.
package ctxlog

import "github.com/rs/zerolog"

// Op returns a child logger carrying the "op" field set to op.
func Op(logger zerolog.Logger, op string) zerolog.Logger {
	return logger.With().Str("op", op).Logger()
}
