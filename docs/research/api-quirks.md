# Provider quirks by API

An index of every provider behaviour pimvault depends on or works around,
grouped by API. The facts and their evidence are the F-rows in
`2026-09-26-initial-research.md` and the dated notes next to it; this file
only says where to look and how each quirk is handled. AGENTS.md says when
to add to it.

Status: **handled** (with the decision or code that handles it), **open**
(a plan item), or **known** (nothing to do).

## Gmail API (mail)

| Fact | Quirk | Status |
|---|---|---|
| F4, F42 | `history.list` start IDs expire, usually after a week, rarely after hours; 404 then | handled: full listing (D50) |
| F33 | `messages.list` pages hold 500 IDs; SPAM and TRASH are excluded unless asked | handled (D17) |
| F42 | an empty `messages.list` with a `fields` mask answers 204 | handled |
| F9, F53 | `messages.insert` skips classification; with the DRAFT label it makes a real draft | handled (D55, D165) |
| F52 | `messages.insert` replaces an invalid Message-ID | handled (D167) |
| F63 | `drafts.create` replaces Date and Message-ID | handled: not used (D165) |
| F70 | `messages.insert` accepts any message but adds Date and Message-ID when missing | handled: repair in the copy (D184) |
| F71 | `labels.create` trims and collapses whitespace; refuses reserved names and over 225 characters | handled: names trimmed like Gmail does; refused ones renamed (D185) |
| F81 | `labels.create` answers 500 for a name over 255 UTF-8 bytes | handled: such names renamed before creating (D185) |
| F76 | chats (label CHAT) not testable on the test account | known, unverified |
| F77, F80 | 15,000 quota units per user per minute documented; 6,000 on pimvault's project, 403 `rateLimitExceeded` over it | handled: retry (D69) |
| F78 | `sizeEstimate` equals the raw size; 5 units per message | handled: dry-run bytes (D189) |
| F82, F83 | one raw download at a time runs at about 6.5 a second; four at once are throttled within seconds | handled: one at a time |
| F84 | `rfc822msgid:` search misses a Message-ID holding an encoded-word; it indexes it decoded, and not at all when decoded it is no msg-id | handled: such copies found by date and sender, matched on the Message-ID (D192) |

## Google Calendar API

| Fact | Quirk | Status |
|---|---|---|
| F4 | sync tokens expire (410) | handled: full listing (D50) |
| F14 | `events.import` makes private copies, no notifications; only `default` event types | handled (D62) |
| F36 | `accessRole` owner is shared; the real owner is `dataOwner` | handled (D20) |
| F54 | import refuses a foreign organizer unless the owner attends | handled (D173) |
| F55 | changed instances attach only when imported after their series | handled |
| F64 | a `dateTime` needs a time zone; each calendar has one | handled (D171) |

## Google People API

| Fact | Quirk | Status |
|---|---|---|
| F4, F77 | sync tokens expire 7 days after the full sync (`EXPIRED_SYNC_TOKEN`) | handled: full listing (D50) |
| F44 | photo URLs are 100-pixel thumbnails unless `=s0` | handled (D151) |

## Google Tasks API

| Fact | Quirk | Status |
|---|---|---|
| F21 | due dates are date-only; no recurrence or reminders; subtasks one level deep | handled (D26, D28) |

## Google OAuth

| Fact | Quirk | Status |
|---|---|---|
| F1, F30 | Testing-status refresh tokens expire after 7 days | avoided: app in production (D156, D201) |
| F24 | device code does not allow Gmail scopes | handled: paste URL (D70) |
| F40 | consent screen and clients are set up in the Console | known |

## Microsoft Graph: mail

| Fact | Quirk | Status |
|---|---|---|
| F4, F34 | delta per folder; `@removed` for deleted or moved; tokens evicted from a cache, 410 then | handled: full listing (D50) |
| F35 | 17 well-known folder names, some refused by some mailboxes | handled |
| F10, F59 | MIME create is always a draft; mailbox import API is 404 for personal accounts | handled: IMAP for writing (D56) |
| F58 | `internetMessageId` filter misses invalid Message-IDs; `sentDateTime` can be off by a second | handled |
| F72 | top-level folder names equal to an existing folder, mail or not, are refused | handled: well-known names renamed (D185) |
| F79 | the listed message size (0x0E08) is 1.2–15× the MIME size; no cheap exact size | known: dry-run says unknown (D189) |

## Microsoft Graph: calendar

| Fact | Quirk | Status |
|---|---|---|
| F6, F38, F43 | no delta for series masters; exceptions only on a GET of the master without `$select` | handled (D53) |
| F13, F48, F50, F57 | attendees get invitations unless `isDraft` | handled (D166) |
| F15 | recurrence narrower than RRULE | handled: expansion (D63, D172) |
| F16, F39 | `iCalUId` per occurrence; `uid` per series; vCal-Uid inside the global object ID | handled (D65) |
| F56 | IANA zones accepted; a given `uid` is ignored | handled (D160) |
| F64, F65 | mailbox time zone needs `MailboxSettings.Read`; personal accounts may only have the working-hours zone | handled (D174) |

## Microsoft Graph: contacts

| Fact | Quirk | Status |
|---|---|---|
| F46 | the default folder is `/contactFolders/contacts`; deeper folders via `childFolders`; delta takes no query options | handled |
| F49, F67, F68 | Outlook.com moves new contacts it takes for duplicates out of the folders; AllContacts still lists them | handled (D176, D177) |

## Microsoft Graph: To Do

| Fact | Quirk | Status |
|---|---|---|
| F22, F31, F47 | checklists as subtasks; the flagged-email list; delta keeps `$expand` without showing it | handled (D26, D148) |

## Outlook.com IMAP

| Fact | Quirk | Status |
|---|---|---|
| F5 | no CONDSTORE or QRESYNC | known: IMAP only for writing |
| F11 | APPEND makes normal messages with the given date | handled (D56) |
| F12, F74 | APPEND accepts about 13.8 MB; above that `BAD Command Argument Error. 11`, from 40 MB `NO Maximum size…`, never TOOBIG | handled: skipped as too big (D57) |
| F66 | `NO User is authenticated but not connected.` while the account's IMAP setting is off | handled (D175) |
| F73 | reserved folder names refused; a hidden Notes folder; long lines, bare LF and 8-bit accepted | handled: reserved and hidden names renamed (D185) |
| F75 | no throttling seen at about 7,800 messages an hour; Office 365 documents throttling answers | handled: the suggested wait (at most 5 minutes) is waited out, 5 times per message |

## Microsoft identity

| Fact | Quirk | Status |
|---|---|---|
| F2, F41 | apps need an Entra directory; personal-account audience is fixed | known |
| F3 | borrowed client IDs get disabled | handled: own apps (D75) |
| F25, F45, F51 | device code cannot grant Graph and IMAP together | handled: browser sign-in (D164) |
| F26, F61, F62 | one sign-in, one token per resource from the refresh token | handled (D161) |

## Filesystems and formats

| Fact | Quirk | Status |
|---|---|---|
| F69 | case-insensitive filesystems merge files whose names differ only in case | handled (D178) |
| F37 | VTIMEZONE needed per TZID | handled |
| F17, F18, F19, F20, F23 | standards and what no file can hold | known |
