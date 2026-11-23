// Package icalfile holds what pimvault's iCalendar writers share: the file
// envelope, encoding with VTIMEZONEs, and property helpers that keep values
// within what RFC 5545 and go-ical accept.
package icalfile

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/emersion/go-ical"

	"github.com/tonimelisma/pimvault/internal/vtimezone"
)

// ProductID identifies pimvault in the files it writes.
const ProductID = "-//pimvault//pimvault//EN"

// New returns an empty VCALENDAR with pimvault's PRODID.
func New() *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, ProductID)
	cal.Props.SetText(ical.PropVersion, "2.0")

	return cal
}

// Encode adds the VTIMEZONEs the calendar's times need and writes it out.
func Encode(cal *ical.Calendar) ([]byte, error) {
	vtimezone.Add(cal)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encode icalendar: %w", err)
	}

	return buf.Bytes(), nil
}

// SetText sets a text property unless value is empty. Line breaks are
// normalized to LF, which go-ical escapes; a CR would make it fail.
func SetText(props ical.Props, name, value string) {
	if value != "" {
		props.SetText(name, Text(value))
	}
}

// Text normalizes line breaks in a text value to LF.
func Text(value string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
}

// SetRaw sets a property whose value is not text, such as an integer or a
// duration, so no VALUE=TEXT parameter is added.
func SetRaw(props ical.Props, name, value string) {
	prop := ical.NewProp(name)
	prop.Value = value
	props.Set(prop)
}

// SetParam sets a parameter unless value is empty. Double quotes and line
// breaks cannot be written in a parameter; they become single quotes and
// spaces.
func SetParam(prop *ical.Prop, name, value string) {
	if value != "" {
		prop.Params.Set(name, strings.NewReplacer(`"`, "'", "\r\n", " ", "\n", " ", "\r", " ").Replace(value))
	}
}
