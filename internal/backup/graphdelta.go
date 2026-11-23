package backup

import (
	"errors"
	"log/slog"

	"github.com/tonimelisma/pimvault/internal/graph"
)

// graphListing turns a round of Graph changes to a collection into a
// listing: removed items become deletions, changed ones are rendered. An
// item that fails to render is logged and kept as failed. A delta link
// Graph no longer accepts becomes errTokenExpired.
func graphListing[T any](
	changes graph.Changes[T], err error, logger *slog.Logger, c collection, kind string,
	id func(*T) string, render func(*T) ([]byte, error),
) (listing, error) {
	if errors.Is(err, graph.ErrDeltaExpired) {
		return listing{}, errTokenExpired
	}

	if err != nil {
		return listing{}, err
	}

	l := listing{items: make(map[string][]byte, len(changes.Changed)+len(changes.Removed)), token: changes.Link}

	for _, removed := range changes.Removed {
		l.items[removed] = nil
	}

	for i := range changes.Changed {
		item := &changes.Changed[i]

		data, err := render(item)
		if err != nil {
			l.fail(id(item))
			logger.Error("back up "+kind, slog.String("collection", c.Path), slog.String(kind, id(item)), slog.String("error", err.Error()))

			continue
		}

		l.items[id(item)] = data
	}

	return l, nil
}
