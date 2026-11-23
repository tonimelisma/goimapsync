# Restore edge cases in other tools (2026-10-25)

How comparable tools handle the restore cases in plan 003, plus live checks
on the Gmail test account. Facts only; decisions live in
`docs/requirements.md`.

## Mail already there

- imapsync treats messages with the same `Message-Id` and `Received`
  headers as duplicates. For servers that change headers it suggests
  `Message-Id` alone, or `Date` plus `Subject`, warning that subjects are
  weak because many are identical or empty. Source:
  https://imapsync.lamiral.info/FAQ.d/FAQ.Duplicates.txt
- Got Your Back (GYB) does not look in the account on restore: it records
  restored message numbers in a local resume database
  (`restored_messages`) and skips those. Restoring again without that
  database, or into another account, writes everything again. It restores
  with `messages.import` and `neverMarkSpam=True`. Source: `gyb.py`,
  https://github.com/GAM-team/got-your-back
- MigrationWiz reads the destination's IMAP UIDs, and its own records, to
  avoid duplicates on later passes; recreating the mailbox record loses
  that protection. Source:
  https://help.bittitan.com/hc/en-us/articles/115008253248
- Cloudiway does not migrate a message twice, based on its Message-ID.
  Source: https://help.cloudiway.com/article/difference-total-migrated-items

## Label and folder name case

- GYB maps system label names case-insensitively (`label.upper()`), and
  matches user labels by exact name, creating any it does not find.
  Source: `gyb.py`.
- imapsync has `--nomixfolders` for a case-sensitive source and a
  case-insensitive destination such as Exchange. Source: imapsync docs,
  https://www.pagure.io/imapsync
- Seen live: Gmail refuses to create a label that differs from an
  existing one only in case (409 "Label name exists or conflicts").

## Contacts already there

- Outlook's import wizard offers "Allow duplicates" (the default),
  "Replace duplicates" and "Do not import duplicates"; duplicates are
  matched by name or email address. Source:
  https://helpdesk.pace.edu/TDClient/213/Portal/KB/Article/3880/Outlook-Client-Contacts-to-Import-in-Outlook
- Google Contacts adds every imported contact; merging is a separate,
  reviewed "Merge & fix" step. Source:
  https://www.techrepublic.com/article/manage-google-contacts/

## Events organized by someone else

- Google's own migration tools import such an event with a warning
  ("User neither attendee or organizer for event"), and the target user
  is shown as an attendee. Source:
  https://knowledge.workspace.google.com/admin/migrate/what-is-migrated
- MigrationWiz (Gmail to Microsoft 365) does not migrate the organizer's
  response status or acceptance emails. Source:
  https://help.bittitan.com/hc/en-us/articles/115008106027
- Seen live: Calendar `events.import` accepts an event whose organizer is
  someone else when the account owner is among the attendees; organizer,
  attendees and the owner's response are kept (compare F54). With the
  Outlook test account as an attendee, nothing arrived in its mailbox or
  calendar within 10 minutes (F64).

## Floating times

- RFC 5545 section 3.3.5: a floating time is the same hour, minute and
  second whatever time zone is observed; a recipient should take it in
  the attendee's current time zone. Source:
  https://www.rfc-editor.org/rfc/rfc5545#section-3.3.5
- Reports of Google Calendar and Outlook importing such times as UTC are
  anecdotal. Sources:
  https://theeventscalendar.com/support/forums/topic/wrong-time-on-google-calendar-import/,
  https://docs.solspace.com/craft/calendar/v3/events/exporting
- Seen live: Calendar `events.import` refuses a `dateTime` without an
  offset or `timeZone` (400 "Missing time zone definition for start
  time"), and accepts one with the calendar's time zone.

## Repeating events without an end

- Google Calendar limits repeating events to 730 occurrences. Source:
  https://support.google.com/calendar/answer/37115
- Graph supports series with no end (`noEnd`). Source:
  https://learn.microsoft.com/en-us/graph/outlook-schedule-recurring-events
- An Oracle sync connector creates occurrences of an endless meeting for a
  limited window ("FanLimit") and adds more as time passes. Source:
  https://docs.oracle.com/cd/G30560_01/books/SSSE/c-Unsupported-Meetings-without-an-End-Date-wc2348706.html
