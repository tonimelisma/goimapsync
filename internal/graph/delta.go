package graph

import (
	"context"
	"fmt"
	"slices"
)

// deltaItem is an item of a delta query: changed, or removed from the
// collection (deleted or moved away).
type deltaItem interface {
	itemID() string
	removed() bool
}

// Changes is one round of changes to a collection: items new or changed in
// it, the IDs of those that left it, and the link to continue from next
// time.
type Changes[T any] struct {
	Changed []T
	Removed []string
	Link    string
}

// delta follows a delta query's pages from target, the first request or a
// stored delta link. Graph can return an item more than once in a round;
// its last appearance is its state. It returns ErrDeltaExpired when the
// link can no longer be used.
func delta[T any, P interface {
	*T
	deltaItem
}](ctx context.Context, c *Client, target string, prefer ...string) (Changes[T], error) {
	var all []T

	for {
		var page struct {
			Value     []T    `json:"value"`
			NextLink  string `json:"@odata.nextLink"`  //nolint:tagliatelle // OData annotation name.
			DeltaLink string `json:"@odata.deltaLink"` //nolint:tagliatelle // OData annotation name.
		}

		err := c.getJSON(ctx, target, &page, prefer...)
		if expired(err) {
			return Changes[T]{}, fmt.Errorf("%w: %w", ErrDeltaExpired, err)
		}

		if err != nil {
			return Changes[T]{}, err
		}

		all = append(all, page.Value...)

		if page.NextLink == "" {
			changes := latest[T, P](all)
			changes.Link = page.DeltaLink

			return changes, nil
		}

		target = page.NextLink
	}
}

// latest keeps each item's last appearance, in the order of those last
// appearances.
func latest[T any, P interface {
	*T
	deltaItem
}](all []T) Changes[T] {
	var changes Changes[T]

	seen := make(map[string]bool, len(all))

	for i := len(all) - 1; i >= 0; i-- {
		item := P(&all[i])
		if seen[item.itemID()] {
			continue
		}

		seen[item.itemID()] = true

		if item.removed() {
			changes.Removed = append(changes.Removed, item.itemID())
		} else {
			changes.Changed = append(changes.Changed, all[i])
		}
	}

	slices.Reverse(changes.Changed)
	slices.Reverse(changes.Removed)

	return changes
}
