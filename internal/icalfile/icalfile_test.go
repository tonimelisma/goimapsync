package icalfile_test

import (
	"testing"

	"github.com/emersion/go-ical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/icalfile"
)

func TestEncode(t *testing.T) {
	cal := icalfile.New()

	event := ical.NewComponent(ical.CompEvent)
	icalfile.SetText(event.Props, ical.PropUID, "x")
	icalfile.SetRaw(event.Props, ical.PropDateTimeStamp, "20260101T000000Z")
	icalfile.SetText(event.Props, ical.PropDescription, "one\r\ntwo\rthree")
	icalfile.SetText(event.Props, ical.PropLocation, "")
	icalfile.SetRaw(event.Props, ical.PropSequence, "3")

	org := ical.NewProp(ical.PropOrganizer)
	org.Value = "mailto:a@example.com"
	icalfile.SetParam(org, ical.ParamCommonName, "A \"B\"\nC")
	icalfile.SetParam(org, ical.ParamRole, "")
	event.Props.Set(org)

	cal.Children = append(cal.Children, event)

	data, err := icalfile.Encode(cal)
	require.NoError(t, err)
	assert.Equal(t, "BEGIN:VCALENDAR\r\nPRODID:-//pimvault//pimvault//EN\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n"+
		"DESCRIPTION:one\\ntwo\\nthree\r\nDTSTAMP:20260101T000000Z\r\nORGANIZER;CN=A 'B' C:mailto:a@example.com\r\n"+
		"SEQUENCE:3\r\nUID:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", string(data))
}
