// Package stdio serves the versioned API over NDJSON on a byte stream.
package stdio

import (
	"context"
	"encoding/json"
	"io"

	"github.com/moureau-dev/menina/internal/api"
)

// Serve reads one request per line and writes one response per line.
func Serve(ctx context.Context, dispatcher *api.Dispatcher, actor api.Actor, in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req api.Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := enc.Encode(dispatcher.Do(ctx, actor, req)); err != nil {
			return err
		}
	}
}
