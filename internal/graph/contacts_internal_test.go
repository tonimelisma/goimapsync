package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ExistingContacts reads every field NewContact writes, or fingerprints of
// existing contacts never match.
func TestContactFieldsAreNewContactsFields(t *testing.T) {
	var names []string

	typ := reflect.TypeFor[NewContact]()
	for i := range typ.NumField() {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "singleValueExtendedProperties" {
			names = append(names, name)
		}
	}

	assert.Equal(t, contactFields, strings.Join(names, ","))
}
