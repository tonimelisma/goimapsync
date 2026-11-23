// Package wintz translates Windows time zone names, which Outlook uses, to
// IANA names with the Unicode CLDR windowsZones table (docs/requirements.md
// D64). The table is embedded unchanged from
// github.com/unicode-org/cldr, common/supplemental/windowsZones.xml.
package wintz

import (
	_ "embed"
	"encoding/xml"
	"sync"
	"time"
)

//go:embed windowsZones.xml
var table []byte

// The table is parsed once, on first use, and loaded zones are kept.
var (
	loadOnce sync.Once         //nolint:gochecknoglobals // lazy cache of the embedded table.
	toIANA   map[string]string //nolint:gochecknoglobals // lazy cache of the embedded table.

	locMu     sync.Mutex                    //nolint:gochecknoglobals // guards locations.
	locations = map[string]*time.Location{} //nolint:gochecknoglobals // zones loaded so far.
)

// Location returns the Go location for a Windows zone name, loading each
// zone once, or false when the name is unknown.
func Location(windows string) (*time.Location, bool) {
	name, ok := IANA(windows)
	if !ok {
		return nil, false
	}

	locMu.Lock()
	defer locMu.Unlock()

	if loc, cached := locations[name]; cached {
		return loc, true
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}

	locations[name] = loc

	return loc, true
}

// IANA returns the IANA zone for a Windows zone name, using the table's
// default ("001") mapping, or false when the name is not in the table.
func IANA(windows string) (string, bool) {
	loadOnce.Do(load)

	name, ok := toIANA[windows]

	return name, ok
}

func load() {
	var doc struct {
		Zones []struct {
			Other     string `xml:"other,attr"`
			Territory string `xml:"territory,attr"`
			Type      string `xml:"type,attr"`
		} `xml:"windowsZones>mapTimezones>mapZone"`
	}

	toIANA = make(map[string]string)

	// The table is part of the binary; a parse failure leaves the map empty
	// and every lookup falls back, which the tests would catch.
	if err := xml.Unmarshal(table, &doc); err != nil {
		return
	}

	for _, z := range doc.Zones {
		if z.Territory == "001" {
			toIANA[z.Other] = z.Type
		}
	}
}
