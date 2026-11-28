# Requirements

What pimvault does today, one EARS sentence per requirement. Each is
`[verified]` by at least one test tagged `Validates: <ID>`, or says
`unverified:` and why no automated test proves it. Future work lives in
GitHub issues, never here; a requirement the code no longer meets changes in
the same PR as the code.

IDs are `PV-NNN` and are never reused. `Aliases:` names the decision rows
(`D…`, `O…`) of the former decision log that the requirement carries;
code, tests and older PRs cite those numbers, and `script/process-check`
resolves every cited alias to a requirement or an ADR. Decisions about how
pimvault is built are ADRs in [`adr/`](adr/); facts with their evidence are
F-rows in [`research/2026-09-26-initial-research.md`](research/2026-09-26-initial-research.md).

pimvault is a headless command-line tool that backs up Google and Microsoft
mail, calendars, contacts and tasks to a local folder in open, standard file
formats, and can use that folder to restore an account or move everything to
the other provider: **backup**, **restore**, **migrate**, with dedupe on the
way in.

## Scope

- **PV-001** The pimvault CLI shall back up, restore and migrate the mail, calendars, contacts and
  tasks of personal Gmail and personal Outlook.com accounts. Aliases: D1, D2, D23. [verified]
- **PV-002** The pimvault CLI shall handle any number of signed-in accounts of each provider.
  Aliases: D16. [verified]
- **PV-003** When backing up Gmail mail, pimvault shall back up Inbox, Sent, Drafts, every user
  label and archived mail, and shall leave out Spam and Trash. Aliases: D17. [verified]
- **PV-004** When backing up Outlook.com mail, pimvault shall back up Inbox, Sent, Drafts, Archive
  and user-created folders, and shall leave out Junk Email, Deleted Items and Conversation
  History. Aliases: D18. [verified]
- **PV-005** The pimvault CLI shall back up an account's whole history, with no cutoff date.
  Aliases: D19.
  unverified: no listing passes a date filter, but no test seeds items old enough to show the
  absence of a cutoff; the end-to-end test backs up the test accounts' full history.
- **PV-006** When backing up calendars, pimvault shall leave out calendars shared with the account
  but owned by someone else. Aliases: D20. [verified]
- **PV-007** When backing up calendars, pimvault shall skip subscribed, read-only calendars (ICS
  feeds) entirely. Aliases: D21. [verified]
- **PV-008** When backing up Google contacts, pimvault shall leave out Google "Other contacts".
  Aliases: D22.
  unverified: pimvault reads only the People API's connections, which never include Other
  contacts; no fake models them.
- **PV-009** The pimvault CLI shall not back up account settings: filters and rules,
  signatures, vacation responders and send-as aliases. Aliases: D24.
  unverified: an absence; no code reads or writes settings.
- **PV-010** When backing up tasks, pimvault shall include completed tasks. Aliases: D27.
  [verified]
- **PV-011** When backing up, pimvault shall skip provider-generated collections: Outlook.com's
  Birthdays and holiday calendars, Google Calendar's contact-birthday events and Microsoft To
  Do's Flagged email list. Aliases: D148. [verified]
- **PV-012** The pimvault CLI shall not keep message flags (read, starred, answered) in the
  archive. Aliases: D9.
  unverified: the `.eml` file is the raw message as downloaded; no test asserts that flags are
  absent.
- **PV-013** The pimvault CLI shall store events, contacts and tasks with standard properties only
  (RFC 5545, RFC 7986, vCard 4.0), leaving out provider fields that have no standard property.
  Aliases: D150. [verified]
- **PV-014** When backing up contacts, pimvault shall embed a photo the user set in the vCard
  `PHOTO` property and skip default placeholder avatars. Aliases: D151. [verified]

## Archive

- **PV-050** The pimvault CLI shall write its archive as a local folder of open formats: mail as
  `.eml` (RFC 5322), events and tasks as iCalendar `.ics` (tasks as `VTODO`), contacts as vCard
  4.0 `.vcf`. Aliases: D6, D7, D25, D42. [verified]
- **PV-051** The pimvault CLI shall record folder membership as directories, with Drafts treated
  as a folder, and shall write no per-item sidecar files. Aliases: D8, D10. [verified]
- **PV-052** The pimvault CLI shall lay out the archive account first:
  `<root>/<account>/mail/<folder>/`, `calendars/<calendar>/`, `contacts/<address book>/` and
  `tasks/<list>/`, where the account directory is the account ID with `:` replaced by `_`.
  Aliases: D29. [verified]
- **PV-053** The pimvault CLI shall store one file per item (one `.eml` per message, one `.ics` per
  event series or task, one `.vcf` per contact) directly in its collection's directory, with no
  date subdirectories. Aliases: D33, D34. [verified]
- **PV-054** The pimvault CLI shall store a recurring event and its exceptions together in one
  `.ics` file. Aliases: D43. [verified]
- **PV-055** The pimvault CLI shall name each archive file by the provider's item ID, writing each
  capital letter as `~` plus its lowercase letter, so items whose IDs differ only in case stay
  apart on case-insensitive filesystems. Aliases: D36, D45, D178. [verified]
- **PV-056** If two calendars, address books or task lists of an account have names that are equal
  or differ only in case, then pimvault shall store the later ones in directories with a ` (2)`,
  ` (3)`, … suffix. Aliases: D178. [verified]
- **PV-057** The pimvault CLI shall store archived Gmail mail (in none of Inbox, Sent, Drafts or a
  user label) in a directory named `Archive`. Aliases: D30. [verified]
- **PV-058** When a Gmail message is in several folder-like places, pimvault shall store it once,
  in the first match of Inbox, Drafts, Sent, user labels, Archive, and record no other
  membership. Aliases: D31, D32. [verified]
- **PV-059** The pimvault CLI shall store nested labels and folders (Gmail `Work/Projects`, Outlook
  Work > Projects) as nested directories (`mail/Work/Projects/`). Aliases: D142. [verified]
- **PV-060** The pimvault CLI shall name the system folder directories `Inbox`, `Sent`, `Drafts`
  and `Archive` for both providers, whatever the account's language. Aliases: D143. [verified]
- **PV-061** The pimvault CLI shall store the default calendar, address book and task list as
  `calendars/Calendar/`, `contacts/Contacts/` and `tasks/Tasks/` for both providers, whatever
  their display names. Aliases: D149. [verified]
- **PV-062** If a folder or label name is too long for a filename (255 bytes once escaped), then
  pimvault shall store it in a directory named by the name cut at a character boundary plus `~`
  and 8 hex characters of a hash of the full name, with the full name in a `.folder-name` file
  in that directory that restore and migrate read. Aliases: D188. [verified]
- **PV-063** The pimvault CLI shall write each file to a temporary name in the target directory,
  `fsync` it and rename it into place, so an interrupted run leaves no half-written file and the
  next run skips files already present. Aliases: D54. [verified]
- **PV-064** When a backup starts, pimvault shall remove temporary files an interrupted run left
  in the archive. Aliases: D58. [verified]
- **PV-065** The pimvault CLI shall create archive and sign-in files readable only by the user
  (files 0600, directories 0700). [verified]

## Backup

- **PV-100** When backing up, pimvault shall mirror the account exactly, one archive file per
  account item, with no dedupe inside the archive. Aliases: D35. [verified]
- **PV-101** When a message moved between folders in the account, backup shall move its file
  between the matching directories. Aliases: D44. [verified]
- **PV-102** When backing up, pimvault shall follow the provider's change tokens where offered
  (Gmail history; Graph delta for Outlook mail per folder, contacts and To Do lists; Google
  Calendar and People sync tokens) and list in full the data that has none (Google Tasks, and
  Outlook calendars, whose recurring series are re-listed on every run). Aliases: D53, D141,
  D146. [verified]
- **PV-103** When a change token has expired, or a Gmail label was renamed (which leaves no
  history), backup shall list everything, download only what is missing and mirror what is
  gone, without downloading existing files again. Aliases: D50. [verified]
- **PV-104** The pimvault CLI shall keep its sync state as JSON files in the state directory
  (`$XDG_STATE_HOME/pimvault/`), so the archive holds only item files. Aliases: D51, D52.
  [verified]
- **PV-105** If an item fails during a backup, then pimvault shall log it on stderr and continue
  with the rest, print one summary line per account on stdout (files, downloaded, moved,
  deleted, failed) and exit 1. Aliases: D144. [verified]
- **PV-106** When an item disappeared from the account, backup shall move its archive file to
  `<archive>/.deleted/<account>/<YYYY-MM-DD>/` under the same type and folder directories and
  delete it from there 30 days later; restore, migrate and verify ignore that area.
  Aliases: D11, D181. [verified]
- **PV-107** If one backup would remove more than 20% of the items of a folder, calendar, address
  book or task list holding at least 50, then pimvault shall remove none of them, back up
  everything else, warn and exit 2, unless `backup --allow-deletions` is given; items moved
  between folders do not count as removals. Aliases: D182. [verified]
- **PV-108** When `backup --dry-run` runs, pimvault shall write nothing and print per account and
  data type how many items the next backup would download, move and remove, with the bytes for
  Gmail mail from Gmail's size estimate and "unknown" for Outlook.com mail, and no time estimate.
  Aliases: D183, D189. [verified]
- **PV-109** When backing up several accounts, pimvault shall back them up at the same time and
  one item at a time within each account, print each account's summary line in the accounts'
  order once it and those before it are done, and in a terminal show every account in progress
  on the status line. Aliases: D191. [verified]
- **PV-110** While a run holds a state directory, a second `backup`, `verify`, `restore` or
  `migrate` on it shall exit 1 at once, naming the running process; a lock left by a crashed run
  is taken over. Aliases: D186. [verified]
- **PV-111** When a provider throttles a request or a quota is hit, pimvault shall wait as long as
  the provider's retry-after says and continue in the same run. Aliases: D69. [verified]
- **PV-112** While a run is in progress, pimvault shall show a live progress line in a terminal
  and log plain periodic progress lines otherwise. Aliases: D77. [verified]

## Verify

- **PV-120** When `verify` runs, pimvault shall compare the archive's item counts per folder,
  calendar, address book and task list with the account, write nothing, print one line per
  collection whose counts differ and a summary line per account, and exit 1 if anything
  differs. Aliases: D81, D154. [verified]
- **PV-121** When `verify` runs, pimvault shall also read every archive file, parsing `.ics` and
  `.vcf` files and checking that an `.eml` file is not empty and has a header, and shall list
  files that fail, count them as unreadable and exit 1. Aliases: D190. [verified]

## Restore and migrate

- **PV-150** When `restore <account>` runs, pimvault shall write that account's whole archive back
  into the same account. Aliases: D12. [verified]
- **PV-151** When `migrate <from> <to>` runs, pimvault shall write the archive of one account into
  another signed-in account, Gmail to Outlook.com or Outlook.com to Gmail, as a one-time move
  that can be run again to resume. Aliases: D13, D14. [verified]
- **PV-152** If the target already has data, restore and migrate shall merge into it and skip the
  items the target already has. Aliases: D60. [verified]
- **PV-153** When restoring or migrating mail, pimvault shall treat a message as already there when
  the target has one with the same `Message-ID`, `Date` and `From` headers. Aliases: D37.
  [verified]
- **PV-154** If an archived message lacks a usable `Date` or a `Message-ID`, then restore and
  migrate shall repair the copy they write, never the archive: the `Date` from the newest
  `Received` header, else the archive file's modification time, and a `Message-ID` derived from
  the message's content, so later runs recognise the copy. Aliases: D40, D184. [verified]
- **PV-155** If Gmail replaced a message's invalid `Message-ID`, then pimvault shall count the
  message as already there when Gmail has one with the same `Date`, `From` and `Subject`.
  Aliases: D167. [verified]
- **PV-156** When a valid `Message-ID` holds an encoded-word, pimvault shall count the message as
  already in Gmail when Gmail has a message within a day of its `Date`, from its sender, with
  the same `Message-ID` (as written or decoded), `Date` and `From`; such a message without a
  valid `Date` is never found. Aliases: D192. [verified]
- **PV-157** When migrating contacts, pimvault shall skip an archived contact whose email address
  a contact the target had before the run already has, write contacts without an email address,
  and write both of two archived contacts that share an address. Aliases: D41, D169. [verified]
- **PV-158** When restore or migrate writes an item, pimvault shall store the archive item's ID in
  a hidden field where the provider has one (Outlook.com contacts and To Do tasks, Google
  contacts) and skip items carrying that ID on later runs; Google Tasks, which has no such
  field, match by title plus due date. Aliases: D160. [verified]
- **PV-159** When restoring or migrating contacts into Outlook.com, pimvault shall count a contact
  as already there when its folder has a contact identical in the fields pimvault writes, and
  shall always write a contact with a photo. Aliases: D176. [verified]
- **PV-160** When Outlook.com moved a contact pimvault wrote out of view as a duplicate, later
  restore and migrate runs shall count it as already there, found through the AllContacts
  folder by its archive ID. Aliases: D177. [verified]
- **PV-161** When restoring or migrating events, pimvault shall skip a series with its exceptions
  when the target has an event with the same UID; on Outlook.com that is the UID backup derives
  from `iCalUId`. Aliases: D65. [verified]
- **PV-162** When restore or migrate runs with `--dry-run`, pimvault shall write nothing and list
  per item what would be written or skipped. Aliases: D61. [verified]
- **PV-163** When writing mail, pimvault shall insert it into Gmail with `messages.insert`, which
  skips spam scanning and classification, and append it to Outlook.com over IMAP. Aliases: D55,
  D56. [verified]
- **PV-164** If a message is larger than Outlook.com accepts, then restore and migrate shall skip
  it, list it in the run's output and keep it in the archive. Aliases: D57. [verified]
- **PV-165** When restoring or migrating mail, pimvault shall map directories by role: `Inbox`,
  `Sent` and `Drafts` to the target's system folders, `Archive` to Gmail's "no label" or
  Outlook.com's Archive folder, and every other directory to an Outlook folder or Gmail label of
  the same name, created if missing. Aliases: D59. [verified]
- **PV-166** When a directory is named like a Gmail system label (Spam, Trash, Important,
  Starred, Unread, in any case), restore and migrate into Gmail shall apply that label instead
  of creating a user label. Aliases: D162. [verified]
- **PV-167** When writing mail into Gmail's Drafts, pimvault shall insert it with the `DRAFT`
  label, which keeps its `Date` and `Message-ID` so later runs skip it, without requesting the
  Gmail compose permission. Aliases: D163, D165. [verified]
- **PV-168** When restoring or migrating, pimvault shall match label and folder names whatever
  their case, as Gmail and Outlook.com treat them. Aliases: D168. [verified]
- **PV-169** If the target refuses a folder or label name, or the name is one of the target's own
  system folders, then restore and migrate shall write it as `<name> (restored)`, cut to the
  target's length limit, and log the rename once; Gmail's trimming of spaces in label names is
  followed silently. Aliases: D185. [verified]
- **PV-170** When restoring or migrating calendars, address books and task lists, pimvault shall
  map them by role, then name: the defaults to the target's defaults, others to a collection of
  the same name, created if missing. Aliases: D152. [verified]
- **PV-171** When restoring or migrating mail, pimvault shall mark the written messages read.
  Aliases: D153. [verified]
- **PV-172** When restore or migrate finishes, pimvault shall have logged each failure and
  size-cap skip on stderr, print one summary line per data type on stdout, and exit 2 if items
  were skipped and 1 if any failed. Aliases: D155, D170. [verified]
- **PV-173** When writing events into Google Calendar, pimvault shall import them with
  `events.import` as private copies that keep their attendees and send no notifications.
  Aliases: D62. [verified]
- **PV-174** When an event organized by someone else goes into Google Calendar, pimvault shall keep
  its real organizer and add the account's owner as an accepted attendee when the event does not
  list them. Aliases: D173. [verified]
- **PV-175** When an event with attendees goes into Outlook.com, pimvault shall write it with its
  attendees and Graph `isDraft: true`, which sends no invitations. Aliases: D166, O31.
  [verified]
- **PV-176** When an event someone else organized goes into Outlook.com, pimvault shall add an
  "Organizer:" line to its body, put meeting and attachment links into the body, and keep the
  series' attendees on its changed occurrences. Aliases: D180, O139. [verified]
- **PV-177** If Outlook.com cannot represent an event's recurrence rule, then pimvault shall write
  the series as individual events within the series' range. Aliases: D63. [verified]
- **PV-178** When a series expanded under PV-177 has no end, pimvault shall stop it two years after
  the run, at most 500 events with the latest kept, and warn when it capped the series.
  Aliases: D172. [verified]
- **PV-179** The pimvault CLI shall translate time zones with the Unicode CLDR windowsZones table
  and write a zone without a mapping in UTC at the same absolute time. Aliases: D64. [verified]
- **PV-180** When an event has a floating time, or a time zone pimvault cannot resolve, pimvault
  shall write it in the target calendar's time zone: for Outlook.com the mailbox time zone, or
  for an account without one its working hours' time zone. Aliases: D171, D174. [verified]
- **PV-181** When restoring or migrating contacts, pimvault shall map contact groups between
  providers: a Google contact label to an Outlook contact category of the same name (vCard
  `CATEGORIES` in the archive). Aliases: D67. [verified]
- **PV-182** When a contact has fields the target cannot hold, pimvault shall append them to the
  contact's notes as `Label: value` lines. Aliases: D68. [verified]
- **PV-183** When restoring or migrating tasks, pimvault shall map a Google subtask to a Microsoft
  To Do checklist item and back. Aliases: D26. [verified]
- **PV-184** When writing tasks into Google Tasks, pimvault shall drop the fields its API cannot
  write (due time, recurrence, reminders) silently; they remain in the archive. Aliases: D28.
  [verified]
- **PV-185** If Outlook.com refuses IMAP because the account's "Let devices and apps use IMAP"
  setting is off, then restore and migrate shall say to turn it on (Settings > Mail > Forwarding
  and IMAP) instead of asking for a new sign-in. Aliases: D175. [verified]

## Sign-in, commands and configuration

- **PV-220** When `login <provider>` runs, pimvault shall print a sign-in URL, take the redirected
  loopback address the user pastes back from any device, and register the account under the
  identity the sign-in returns; there is no device-code flow and no `account add`.
  Aliases: D70, D71, D123, D164. [verified]
- **PV-221** When signing in, pimvault shall request read and write permissions at once, so one
  sign-in covers backup, restore and migrate; for Outlook.com one sign-in grants Graph, IMAP and
  `MailboxSettings.Read`. Aliases: D135, D161, O138. [verified]
- **PV-222** The pimvault CLI shall identify an account as `provider:email` from the sign-in, with
  the email in lower case, and shall accept any unique prefix or substring of it on the command
  line. Aliases: D122, D132. [verified]
- **PV-223** The pimvault CLI shall treat the set of sign-in files in the config directory as the
  account registry: `account list` scans them and `account remove` deletes one, keeping the
  archive and state. Aliases: D125. [verified]
- **PV-224** The pimvault CLI shall offer the commands `login <provider>`, `backup [account...]`,
  `verify [account...]`, `restore <account>`, `migrate <from> <to>` and `account list|remove`,
  with no `init` command. Aliases: D76, D95, D124. [verified]
- **PV-225** The pimvault CLI shall resolve each directory with the precedence flag > environment
  variable > `config.toml` > XDG default: `--archive`/`PIMVAULT_ARCHIVE`/`archive` (default
  `$XDG_DATA_HOME/pimvault/`), `--state`/`PIMVAULT_STATE`/`state` (default
  `$XDG_STATE_HOME/pimvault/`) and `--config`/`PIMVAULT_CONFIG` (default
  `$XDG_CONFIG_HOME/pimvault/config.toml`), with sign-ins next to the config file.
  Aliases: D72, D96, D98, D99, D115, D116. [verified]
- **PV-226** The pimvault CLI shall read `config.toml` (TOML) holding only the `archive` and `state`
  overrides and the app client overrides, never rewrite it, and stop with an error naming any
  unknown or misspelled key. Aliases: D73, D127, D128. [verified]
- **PV-227** If a directory in `config.toml` is neither absolute nor starts with `~/`, then pimvault
  shall refuse it; flag and environment values may be relative to the working directory.
  Aliases: D131. [verified]
- **PV-228** If a config file named with `--config` or `PIMVAULT_CONFIG` does not exist, then
  pimvault shall fail; only the default config file may be missing, in which case the defaults
  apply, and directories are created when first needed. Aliases: D133. [verified]
- **PV-229** The pimvault CLI shall store OAuth refresh tokens and app client secrets as plain
  files in the config directory, mode 0600. Aliases: D74. [verified]
- **PV-230** The pimvault CLI shall use the client IDs of its own apps (and Google's non-confidential
  desktop client secret) compiled in, unless `config.toml` overrides them with
  `google_client_id` and `google_client_secret` (together) or `microsoft_client_id`.
  Aliases: D136. [verified]
- **PV-231** The pimvault CLI shall accept `--dry-run`, `--log-format=text|json`, `-v`/`--verbose`
  and `-q`/`--quiet`, and log plain text on stderr by default or one JSON object per line with
  `--log-format=json`. Aliases: D78, D117. [verified]
- **PV-232** The pimvault CLI shall exit 0 on success, 1 on an error or a failed item or account, 2
  when it finished with skipped items (or withheld mass deletions), and 3 when an account needs
  `login` again. Aliases: D79, D93. [verified]
- **PV-233** If an account's sign-in is expired or revoked, then pimvault shall skip that account
  with a message to run `login`, continue with the other accounts and exit 3. Aliases: D80.
  [verified]
- **PV-234** When `pimvault --version` runs, pimvault shall print its version. Aliases: D187.
  [verified]
- **PV-235** When a token source refreshes an access token, pimvault shall save the refreshed
  token once, and if the save fails, log it and go on with the refreshed token. Aliases: D203.
  [verified]

## Releases

- **PV-270** When the CI run of a push to `main` succeeds and its commit is still `main`'s head,
  the release workflow shall tag that commit with the next patch version of the series in
  `VERSION` (above every existing tag and v0.1.2) and publish static Linux binaries (amd64,
  arm64), `SHA256SUMS` and a Sigstore build provenance attestation signed by
  `.github/workflows/release.yml` on `refs/heads/main`. Aliases: D194, D196, D197, D198.
  [verified]
- **PV-271** The release workflow shall build reproducibly: the exact Go version from `go.mod`,
  `-trimpath`, no cache and no secret beyond `GITHUB_TOKEN`. Aliases: D197.
  unverified: no job builds a release twice and compares the bytes; `script/release.sh` pins the
  inputs, and anyone can rebuild a tag to check.
