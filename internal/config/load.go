package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// decodeFile strictly decodes one TOML file into v.
// A missing file is not an error: both config layers are optional.
// Decode failures carry file:line:column so the user can jump
// straight to the offending key (the reason go-toml was chosen, D10).
func decodeFile(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return positionedError(path, err)
	}
	return nil
}

// positionedError rewrites go-toml's error types into
// "<file>:<row>:<col>: message" form.
func positionedError(path string, err error) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) && len(strict.Errors) > 0 {
		// Report the first unknown key; one fix at a time
		// beats a wall of positions.
		e := strict.Errors[0]
		row, col := e.Position()
		return fmt.Errorf("%s:%d:%d: unknown key %q (not part of wt's config)",
			path, row, col, strings.Join(e.Key(), "."))
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		row, col := decode.Position()
		return fmt.Errorf("%s:%d:%d: %s", path, row, col, decode.Error())
	}
	return fmt.Errorf("%s: %w", path, err)
}
