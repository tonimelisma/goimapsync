# pimvault requirements

This document records only decisions the owner has made (D-rows). Anything not
yet decided is listed under Open decisions (O-rows), with the known options and
no pre-selected answer. Facts cited as F-numbers are in
`docs/research/2026-09-26-initial-research.md`.

## Summary

A headless command-line tool that syncs Google and Microsoft mail, calendars,
contacts and tasks to a local folder in open, standard file formats, and can use
that folder to restore an account or move everything to the other provider.

Capabilities: **backup**, **restore**, **migrate**, **dedupe**.

## Decisions

### Scope
| # | Decision |
|---|---|
| D1 | Data types: mail, calendars, contacts, tasks (tasks added via O8). |
| D2 | v1 accounts: personal Gmail, personal Outlook.com. |
| D3 | Runs headless on a Linux server. No GUI. |
| D4 | Built as a single Go binary with its own provider adapters (not a wrapper around other tools). |
| D5 | OAuth with the owner's own app registrations (Google Cloud project, Microsoft Entra app) is acceptable. |
| D6 | The tool's output is a local folder. |
| D16 | Any number of accounts per provider (O1). |
| D17 | Gmail folders backed up: Inbox, Sent, Drafts and archived mail. Spam and Trash are excluded (O2). |
| D18 | Outlook.com folders backed up: Inbox, Sent, Drafts, Archive and user-created folders. Junk Email, Deleted Items and Conversation History are excluded (O3). |
| D19 | All history is backed up; no cutoff date (O4). |
| D20 | Calendars shared with the owner but owned by someone else are excluded (O5). |
| D21 | Subscribed (read-only ICS feed) calendars are skipped entirely (O6). |
| D22 | Google "Other contacts" are excluded (O7). |
| D23 | Tasks (Google Tasks, Microsoft To Do) are in scope (O8). |
| D24 | Account settings (filters/rules, signatures, vacation responder, send-as aliases) are out of scope (O9). |
| D25 | Tasks are stored as iCalendar `VTODO`, one `.ics` file per task, in a directory per task list (O57). |
| D26 | Subtasks map between providers: Google subtask ↔ Microsoft To Do checklist item (O58). |
| D27 | Completed tasks are included (O60). |
| D28 | Moving tasks into Google: fields the Google Tasks API cannot write (due time, recurrence, reminders) are dropped silently; they remain in the archive (O59). |
| D29 | Archive structure is account first: `<root>/<account>/mail/<folder>/`, `<root>/<account>/calendars/<calendar>/`, `<root>/<account>/contacts/<address book>/`, `<root>/<account>/tasks/<list>/` (O10). |
| D30 | Archived Gmail mail (not in Inbox, Sent or Drafts) goes in a directory named `Archive` (O11). |
| D31 | A Gmail message in several folder-like places is stored once, in the first matching directory of a fixed precedence order; other memberships are not recorded (O12). This also settles O13: no label list is written into the `.eml`. |
| D32 | Precedence order for D31: Inbox > Drafts > Sent > user labels > Archive (O12a). |
| D33 | Mail folders are flat: all `.eml` files directly in the folder's directory, no date subdirectories (O14a). |
| D34 | One file per item for everything: one `.ics` per event and one `.vcf` per contact, in a directory per calendar / address book, matching D25 for tasks (O15). |
| D35 | Mail dedupe happens only at migrate/restore: messages the target already has are skipped. The archive mirrors the account exactly, one file per account item (O38; revised from backup + migrate/restore). |
| D36 | `.eml` files are named by the provider's message ID (Gmail message ID; Outlook immutable ID) (O14; capitals marked by D178). |
| D37 | Two emails are duplicates when they have the same `Message-ID` and the same `Date` and `From` headers (O39). |
| D40 | Emails without a `Message-ID` are never deduped (O40). (revised by D184 for restore and migrate). |
| D41 | Contacts in a migrate are skipped when the target already has a contact with the same email address; contacts without an email address are always written (O42). |
| D42 | Contacts are written as vCard 4.0 (O16). |
| D43 | A recurring event and its exceptions are stored together in one `.ics` file (O17). |
| D44 | A message moved between folders in the account is moved between directories in the archive (O18). |
| D45 | Event, contact and task files are named by the provider's ID, like mail (O14b). |
| D46 | Gmail mail is read through the Gmail API (O19a). |
| D47 | Outlook.com mail is read through Microsoft Graph (O19b). |
| D48 | The tool is a one-shot command: each run syncs once and exits; scheduling is external (O20). |
| D49 | Sync frequency is the user's choice; the tool warns when more than 6 days have passed since the last successful run (O21). (dropped by D147). |
| D50 | When a change token has expired (change tokens per D146), the tool does an automatic full resync: list everything, download what is missing, mirror what is gone; existing files are not re-downloaded (O22). |
| D51 | Sync state lives in the XDG state directory: `$XDG_STATE_HOME/pimvault/` (default `~/.local/state/pimvault/`). The archive holds only `.eml`/`.ics`/`.vcf` files (O47b-3; replaces the earlier choice of a hidden directory in the archive). |
| D52 | State is stored as JSON files (O23b). |
| D53 | Outlook calendars: recurring series are re-listed in full on every run (O25). |
| D54 | Crash safety: each file is written to a temporary name and renamed into place; an interrupted run leaves no half-written files. The next run re-lists and skips files already present. No progress checkpoints (O24). |
| D55 | Mail is written into Gmail with `messages.insert` (no spam scanning or classification) (O26). |
| D56 | Mail is written into Outlook.com with IMAP APPEND, using the IMAP OAuth scope on the same Microsoft app (O27). |
| D57 | Messages larger than Outlook.com's size cap are skipped and listed in the run report; they stay in the archive (O28). |
| D58 | Temporary files left by an interrupted run are removed at the start of the next run (O24a). |
| D59 | Restore/migrate maps directories by role: Inbox, Sent and Drafts to the target's system folders; `Archive` to Gmail "no label" or Outlook's Archive folder; every other directory becomes an Outlook folder or Gmail label of the same name, created if missing (O29). |
| D60 | If the target already has data, restore/migrate merges into it and skips items the target already has (O30). |
| D61 | Restore/migrate has a dry-run mode that writes nothing and lists, per item, what would be written or skipped (O36). |
| D62 | Events going into Gmail are written with `events.import` as private copies: attendees kept, no notifications (O32). |
| D63 | A recurrence rule Outlook cannot represent is expanded into individual events within the series' range (O33). |
| D64 | Time zones are translated with the Unicode CLDR windowsZones table; zones without a mapping are written in UTC at the same absolute time (O34). |
| D65 | On restore/migrate an event (series with its exceptions) is skipped when the target has an event with the same UID; on Outlook the Graph `uid` property is used, not `iCalUId` (O41). |
| D66 | O31 is decided after prototypes: EWS `SendToNone` and Graph `isDraft` are tested against a test Outlook.com account before restore is built. O31 stays open until then (decided by D166). |
| D67 | Contact groups map between providers: Google contact label ↔ Outlook contact category of the same name (vCard 4 `CATEGORIES` in the archive) (O35a). |
| D68 | Contact fields the target cannot hold are appended to the contact's notes as `Label: value` lines (O35b). |
| D69 | When a provider throttles or a quota is hit, the tool honors the provider's retry-after and continues in the same run (O37a). |
| D70 | Google sign-in: the tool prints a sign-in URL; the user signs in on any device and pastes the redirected `127.0.0.1` URL back into the tool (O45a). |
| D71 | Microsoft sign-in is the paste-the-redirected-URL flow, like Google's (O45b; revised by D164: device code is gone). |
| D72 | The config file is `$XDG_CONFIG_HOME/pimvault/config.toml` (default `~/.config/pimvault/config.toml`) (O47b-2; replaces the earlier choice of the archive's hidden directory). |
| D73 | Config format is TOML (O43b). |
| D74 | OAuth refresh tokens and app client secrets are stored as plain files in `$XDG_CONFIG_HOME/pimvault/`, mode 0600 (O47b-4; replaces the earlier choice of the archive's hidden directory). |
| D75 | The Google Cloud project and Microsoft Entra app are created by Claude with the provider CLIs when implementation reaches that point (O46). Whether each step is possible from the CLI is to be checked then.  Reconfirmed: a new app, not onedrive-go's. |
| D76 | Separate commands: `backup`, `restore <account>` (archive → same account), `migrate <from> <to>` (archive of one account → another account), plus `login` (O47; command names settled in D95, flags still open). |
| D77 | Progress: a live progress bar when run in a terminal; plain periodic lines otherwise (O37b). |
| D78 | Logs are plain text on stderr by default; a flag switches to one JSON object per line (O48). |
| D79 | Distinct exit codes (e.g. success, error, completed with skipped items, sign-in needed); exact values still open (O49). |
| D80 | An account whose token is expired or revoked is skipped with a message to run `login <account>`; other accounts continue; the run exits non-zero (O50). |
| D81 | A `verify` command compares archive item counts per folder/calendar/list with the account and reports differences, without writing (O51). (output and exit code in D154; extended by D190). |
| D82 | Google Calendar is read through the Calendar API (JSON, converted to `.ics`) (O19c). |
| D83 | Google Contacts are read through the People API (JSON, converted to vCard 4.0) (O19d). |
| D84 | IMAP (used only for APPEND into Outlook.com) uses emersion/go-imap v2 (beta) (O53a). |
| D85 | Microsoft Graph is called as plain REST/JSON with net/http, not the SDK (O53b). |
| D86 | Go 1.27; Linux only (O52). |
| D87 | Google APIs are called with the official Go client (`google.golang.org/api`, `golang.org/x/oauth2`) (O53c; the API client part is revised by D140). |
| D88 | iCalendar and vCard use emersion go-ical and go-vcard (O53d). |
| D89 | License: MIT (O55). |
| D90 | Testing: end-to-end runs against real test accounts (throwaway Gmail and Outlook.com), plus unit tests following onedrive-go's rules: testify assertions, fakes/mocks at I/O seams, failure injection, golden files, no `time.Sleep` (O54; revised to add unit tests). |
| D91 | Repository layout: `cmd/pimvault/` + `internal/<package>/`, plus code copied from onedrive-go under `pkg/<name>/` as separate Go modules (see D111) (O56a). (module layout revised by D145). |
| D92 | CI: GitHub Actions on every push/PR running build, tests (with `-race`), `go vet` and a linter (O56b). |
| D93 | Exit codes: 0 success; 1 error; 2 finished with skipped items; 3 one or more accounts need `login` (O49a). |
| D94 | Real-account end-to-end tests run locally only; CI runs build, vet, lint and tests that need no accounts (O54a). (refined by D157). |
| D95 | Commands: `login <provider>`, `backup [account...]`, `verify [account...]`, `restore <account>`, `migrate <from> <to>`, `account remove|list` (O47a; revised by D123 and D124: no `init`, no `account add`). |
| D96 | Default archive location is the XDG data directory: `$XDG_DATA_HOME/pimvault/` (default `~/.local/share/pimvault/`), overridable by environment variable and config file (O47b-1). |
| D97 | The tool is named `pimvault` (O0). |
| D98 | Directory overrides precedence: command-line flag > environment variable > config file > XDG default (O47c-1). |
| D99 | Environment variables use a `PIMVAULT_` prefix; standard `XDG_*` variables are honored for the defaults (O47c-2). |
| D115 | Directory flags: `--archive <dir>`, `--config <file>`, `--state <dir>`; tokens follow the config directory (O47d-1). |
| D116 | Environment variables mirror the flags: `PIMVAULT_ARCHIVE`, `PIMVAULT_CONFIG`, `PIMVAULT_STATE` (O47d-2). |
| D117 | Other flags: `--dry-run`, `--log-format=text|json`, `-v`/`--verbose`, `-q`/`--quiet` (O47d-3; `login --paste-url` removed by D164). |

### Local archive
| # | Decision |
|---|---|
| D7 | Open, generic formats: mail as `.eml` (RFC 5322), events as `.ics` (iCalendar), contacts as `.vcf` (vCard). |
| D8 | Folder information is stored the standard way: as directories. No per-item sidecar files. |
| D9 | Message flags are not preserved: no read, starred or answered state. |
| D10 | Drafts are treated as a folder. |
| D11 | Deletions in the account are mirrored in the archive immediately.  Reconfirmed: a restore cannot bring back items deleted in the account; recovering deleted items is not this tool's job. (revised by D181, D182). |

### Restore, migrate, dedupe
| # | Decision |
|---|---|
| D12 | Restore granularity: whole account. |
| D13 | Migrate is a one-time move (resumable), not an ongoing sync. |
| D14 | Migrate works in both directions: Gmail → Outlook.com and Outlook.com → Gmail. |
| D15 | Dedupe uses the keys the standards already define (mail `Message-ID`, iCalendar/vCard `UID`) rather than a custom hashing scheme. |

### Repository and process setup
Applied once the product requirements are complete. The sister repos `tonimelisma/Otos` and `tonimelisma/onedrive-go` are the sources.

| # | Decision |
|---|---|
| D100 | Documentation follows a lighter Otos layout: `docs/requirements.md` (the decisions), `docs/research/` (the facts), `docs/plans/` (deleted when done). ADRs and `docs/architecture.md` are added once there is code and a real architectural decision to justify. `PRD.md` is split into these. |
| D101 | Local checks run through one simple script (e.g. `script/check.sh`): format, lint, build, race tests. |
| D102 | `AGENTS.md` is the real instruction file; `CLAUDE.md` is a symlink to it; no `GEMINI.md`. |
| D103 | `.claude/settings.json` (hooks, e.g. a setup hook installing Go 1.27) and `.claude/skills/` are committed; only `.claude/settings.local.json` is gitignored (revised: cloud sessions clone fresh, so ignored files never exist there). |
| D104 | Principles copied into `AGENTS.md`: blank slate and no backward compatibility; simplicity as a requirement and Pike's rules; no guessing and no fabricated facts; one user action one implementation path; the account is the source of truth and the archive and state are rebuildable projections; onedrive-go's engineering philosophy, anti-fizzbuzz guardrails and repo-ownership rules. |
| D105 | Only project-specific Go conventions go into `AGENTS.md`: `verb noun: %w` error wrapping, partial failure as a first-class result, atomic write with fsync, preserve fields you don't understand, mandatory pagination, no `time.Sleep` in tests. Standard Go practice and anything golangci-lint enforces is left out (revised from onedrive-go's full list). |
| D106 | Process: increments; PR-only changes to `main`; before coding, a short task brief (outcome, non-goals, how it will be verified, data-safety note for anything touching live accounts); before each PR, a fresh-context `/code-review` whose correctness and requirement findings are acted on; a red `main` blocks merges; doc-only light path; never delete others' work. Done = check script green on the final tree with its output shown, docs updated, PR merged; final report: what changed, what didn't, open risks. Branch prefixes and worktrees are not used (revised). |
| D107 | Documentation rules: capture owner guidance in its proper doc in the same increment and restate non-trivial requirements for confirmation; dated research notes with sources; a live-incidents ledger is added once real-account tests exist. The recent-PR review-comment sweep is not copied. |
| D108 | Tooling copied and adapted: `.golangci.yml` (onedrive-go), CI workflow reduced to build, `-race` tests, vet and lint, `dependabot.yml`, `.gitignore`, `.editorconfig`, PR template (Otos, stripped of macOS items), `.env.example`, a test-credential bootstrap script for Google and Microsoft test accounts. |
| D109 | Code copied from onedrive-go as a starting point: `tokenfile`, `retry`, `graphtransport`, Graph auth and client core, `errclass`, `logfile`. (`config` was selected first, then dropped by O57b.) |
| D110 | Each copied package becomes its own Go module (own `go.mod`) under `pkg/<name>/`, one module per package, used via `go.work`; they must not import pimvault code, so they can later move to their own repository. (module layout revised by D145). |
| D111 | The `perf` timing hook in `retry` and `graphtransport` is dropped when copying. |
| D112 | From onedrive-go's `graph` package only the auth flows (device code, browser/loopback), token source and client core (requests, retry, redaction, errors) are copied; drive endpoints are not. |
| D113 | `fsroot` is not copied; the few `fsroot` calls in the copied `tokenfile` and `logfile` are replaced with plain `os`/`filepath` code (O57a). |
| D114 | pimvault's config package is written from scratch; onedrive-go's `config` is not copied (O57b). |
| D118 | Once implementation starts, the owner decides anything user-visible, any data-safety behavior, package boundaries and dependencies; the agent decides implementation details within those and reports them. (package boundaries revised by D139). |
| D119 | `AGENTS.md` includes: research before asking; ask the owner decisions with the AskUserQuestion tool, with options, facts and tradeoffs and the agent's recommendation marked first; run the check script and show its output before saying work is done, with evidence from the final tree (revised: recommendations are now wanted). |
| D120 | CLI library: `github.com/spf13/cobra` (v1.10.x; brings `github.com/spf13/pflag`). |
| D121 | TOML library: `github.com/BurntSushi/toml` (v1.6.x). |
| D122 | An account is identified as `provider:email` from the sign-in (e.g. `gmail:user@gmail.example.com`, `outlook:user@outlook.example.com`); unique even when a Microsoft account signs in with a Gmail address. The archive directory name replaces `:` with `_` (`gmail_user@gmail.example.com`). Commands accept any unique prefix or substring. |
| D123 | `login <provider>` signs in and registers the account under the identity the sign-in returns; there is no `account add` (revises D95). |
| D124 | No `init` command: directories are created when first needed and the config file is optional, with defaults applying without one (revises D95). |
| D125 | The set of token files in `~/.config/pimvault/` (D74) is the account registry: `account list` scans them, `account remove` deletes one. `config.toml` holds only the owner's settings and is never rewritten by the tool. |
| D126 | Foundation packages: `internal/cli` (cobra commands, output), `internal/config` (directory resolution flag > env > config > XDG, `config.toml` loading), `internal/account` (`provider:email` identity and the token-file registry). (starting layout; the agent owns package layout from D139 on). |
| D127 | `config.toml` holds only `archive` and `state` directory overrides for now; other settings are flags. Keys are added only when a feature needs one. |
| D128 | Unknown or misspelled keys in `config.toml` stop the run with an error naming the key. |
| D129 | Only `tokenfile` is copied into `pkg/` in the foundation increment; the other onedrive-go packages (D109) arrive with the increments that use them. (module layout revised by D145). |
| D130 | PRs are squash-merged by the agent once CI is green, without asking the owner: one commit per increment on `main`. |
| D131 | Directory values in `config.toml` must be absolute or start with `~/` (expanded to the home directory); flag and environment values may be relative to the working directory. |
| D132 | Account IDs store the email in lower case, so one mailbox is always one account. |
| D133 | A config file named with `--config` or `PIMVAULT_CONFIG` must exist; only the default location may be missing. |
| D134 | OAuth uses the owner's fork `github.com/tonimelisma/oauth2` (a `replace` for `golang.org/x/oauth2`, as onedrive-go does) for its `OnTokenChange` hook, until golang/go#77502 lands upstream. |
| D135 | `login` requests read and write permissions at once, so one sign-in covers backup, restore and migrate. |
| D136 | The client IDs of pimvault's own apps (and Google's non-confidential desktop client secret) are compiled in; `config.toml` can override them with `google_client_id`, `google_client_secret` and `microsoft_client_id`. |
| D137 | Login packages: `pkg/oauthflow` (generic device-code and paste-URL flows with PKCE (device code removed by D164) and a token source that saves refreshed tokens, adapted from onedrive-go) and `internal/login` (provider endpoints, scopes, client IDs, and finding the account's email after sign-in). (starting layout; the agent owns package layout from D139 on). (module layout revised by D145). |
| D139 | Package and module layout is the agent's decision and grows with the code; the agent never asks the owner about it and reports it in the PR. The owner still decides anything user-visible, data safety and third-party dependencies (revises D118). |
| D140 | Google APIs are called as plain REST/JSON with `net/http`, like Graph (D85); `google.golang.org/api` is not used. `golang.org/x/oauth2` stays (revises D87). |
| D141 | Gmail mail backup lists all message IDs plus the IDs in Inbox, Drafts, Sent and each user label on every run, derives each message's directory from that, and downloads only missing files. No change token is stored for Gmail mail. (change detection revised by D146; full listing is the fallback). |
| D142 | Nested labels and folders become nested directories: Gmail label `Work/Projects` or Outlook folder Work > Projects is `mail/Work/Projects/`. (long names per D188). |
| D143 | System folder directories are `Inbox`, `Sent`, `Drafts` and `Archive` for both providers, in English whatever the account language. |
| D144 | `backup` logs each failed item on stderr and continues with the rest; at the end it prints one summary line per account on stdout (files, downloaded, moved, deleted, failed) and exits 1 if any item or account failed. |
| D145 | pimvault is one Go module. Code copied from onedrive-go lives in `internal/` like everything else; it moves to its own module only when it actually moves to its own repository (revises D91, D110, D129, D137). |
| D146 | Change detection uses the provider's change tokens where offered: Gmail history, Graph delta (Outlook mail per folder, contacts, To Do lists), Google Calendar and People sync tokens. Data without tokens is fully listed each run (Google Tasks; Outlook series per D53). An expired token, or a Gmail label rename (which produces no history), triggers the full listing (D50, D141). Tokens are stored in the state directory (D51, D52) (revises D141). |
| D147 | The 6-day warning is dropped: expired tokens are handled by D50 and missed runs are the scheduler's concern (revises D49). |
| D148 | Provider-generated collections are skipped: Outlook's Birthdays and holiday calendars, Google contact-birthday events, and Microsoft To Do's Flagged email list. |
| D149 | The default calendar, address book and task list are stored as `calendars/Calendar/`, `contacts/Contacts/` and `tasks/Tasks/` for both providers, whatever their display names. |
| D150 | Events, contacts and tasks are stored with standard properties only (RFC 5545, RFC 7986, vCard 4.0); provider fields without a standard property are not stored. |
| D151 | Contact photos set by the user are embedded in the `.vcf` `PHOTO` property; default placeholder avatars are skipped. |
| D152 | Restore/migrate maps calendars, address books and task lists by role, then name: the defaults go to the target's defaults; other directories go to a collection with the same name, created if missing (like D59). |
| D153 | Restored and migrated mail is marked read. |
| D154 | `verify` prints one line per folder, calendar, address book or list whose counts differ, then a summary line per account, and exits 1 if anything differs. |
| D155 | Restore/migrate output matches backup (D144): failures and size-cap skips logged per item on stderr, one summary line per data type on stdout; exit 2 if items were skipped, exit 1 if any failed. |
| D156 | The Google app is published "In production" without Google verification: sign-in shows an unverified-app warning; refresh tokens do not expire after 7 days as they do in Testing status. (for now superseded by D159). |
| D157 | End-to-end tests run in the agent's cloud environment, with test-account tokens stored as environment secrets; never in GitHub CI. (replaced by D193). |
| D158 | Test-account tokens for end-to-end tests are kept in Google Secret Manager in pimvault's Google Cloud project. Each environment (local, the agent's cloud environment, CI if ever used) holds one service-account credential as an environment variable; the tests read tokens from the store and save refreshed ones back. |
| D159 | For now the Google app stays in Testing status, with the test accounts added as test users; Google expires their sign-ins after 7 days, so they are signed in again weekly. Publishing (D156) waits until pimvault is used for real accounts. |
| D160 | Restore and migrate store the archive item's ID in a hidden field of each item they write where the provider has one (Outlook contacts and To Do tasks, Google contacts), and skip items carrying that ID on later runs, so re-runs do not write them again. Items already in the account before still follow D40 and D41. Google Tasks has no hidden field: tasks match by title plus due date. Mail without a Message-ID stays as D40 says. |
| D161 | Signing in to an Outlook.com account is one sign-in that grants everything pimvault needs, mail writing (IMAP) included (O138). |
| D162 | Restore/migrate writes a directory named like a Gmail system label (Spam, Trash, Important, Starred, Unread, in any case) into that label instead of creating a user label. |
| D163 | Mail restored or migrated into Gmail's Drafts is created as drafts through the drafts API, with the Gmail compose permission that needs (revised by D165). |
| D164 | Outlook.com sign-in is browser only: the user opens the sign-in URL and pastes the redirected address back. Device code sign-in and the `login --paste-url` flag are removed (revises D71, D117), because device code cannot grant Graph and IMAP in one sign-in (F51, F62). |
| D165 | Mail restored or migrated into Gmail's Drafts is inserted with the DRAFT label, which keeps its Date and Message-ID so re-runs skip it; the Gmail compose permission is not requested (revises D163; F63). |
| D166 | Calendar events with attendees go into Outlook.com with their attendees and Graph `isDraft: true`, which sends no invitations (decides O31; F48, F50, F57). |
| D167 | Gmail replaces a Message-ID that is not valid (F52); such a message counts as already there when Gmail has one with the same Date, From and Subject. |
| D168 | Label and folder names match whatever their case when restoring, as Gmail and Outlook treat them. |
| D169 | D41 applies in migrate only, against contacts the target had before the run; two archived contacts sharing an address are both written. |
| D170 | Restore and migrate print one summary line per data type (confirms D155). |
| D171 | A floating time, or one in a time zone pimvault cannot resolve, is written in the target calendar's time zone (RFC 5545 floating time). |
| D172 | A repeating event expanded under D63 without an end stops two years after the run, at most 500 events (the latest kept), with a warning when capped. |
| D173 | An event organized by someone else is restored into Google Calendar with its real organizer; the account owner is added as an accepted attendee when the event does not list them, as Google's own migration tool does (revises the description fallback; F54, F64). |
| D174 | Outlook.com sign-in also asks for `MailboxSettings.Read`, so floating times go into Outlook in the account's time zone (D171): the mailbox time zone, or for an account without one its working hours' time zone (F65). |
| D175 | When Outlook.com refuses IMAP because the account's "Let devices and apps use IMAP" setting is off, restore and migrate say to turn it on (Settings > Mail > Forwarding and IMAP) instead of asking for a new sign-in (F66). |
| D176 | Restore and migrate into Outlook.com count a contact as already there when its folder has a contact identical in the fields pimvault writes, since Outlook.com removes such a copy itself (F67). A contact with a photo is always written. |
| D177 | A contact pimvault wrote that Outlook.com later moved out of view as a duplicate counts as already there on later restore and migrate runs, found through the AllContacts folder by its archive ID (F68). |
| D178 | Archive file names write each capital letter of the provider ID as `~` plus its lowercase letter (`AAkALg…` → `~a~ak~alg…`), so items whose IDs differ only in case stay apart on case-insensitive filesystems (macOS, exFAT, most SMB shares); folder directories whose names differ only in case get " (2)" suffixes as duplicates do (F69; revises D36). |
| D179 | The end-to-end test runs in the agent's cloud environment before every PR (refines D157). Only the throwaway test accounts are used, never the owner's own account; account emails are redacted in logged output. (revised by D193). |
| D180 | Events going into Outlook.com that someone else organized get an "Organizer:" line in the body, since Graph makes the account owner the organizer; meeting links and attachment links go into the body; changed occurrences of a series keep the series' attendees (decides O139). |
| D181 | When an item disappears from the account, backup moves its archive file to `<archive>/.deleted/<account>/<YYYY-MM-DD>/` (same type and folder directories below it) instead of deleting it, and deletes it from there 30 days later. Restore, migrate and verify ignore that area (revises D11). |
| D182 | If one backup run would remove more than 20% of the items of a folder, calendar, address book or task list holding at least 50, it removes none of them, backs up everything else, warns and exits 2; `backup --allow-deletions` lets such removals through. Items moved between folders do not count as removals (revises D11). |
| D183 | `backup --dry-run` writes nothing and prints, per account and data type, how many items (for mail also how many bytes) the next backup would download, move and remove. It gives no time estimate: throughput is not measured. (revised by D189). |
| D184 | Restore and migrate repair a message in the copy they write, never in the archive: a missing or unparseable `Date` is taken from the message's newest `Received` header, else from the archive file's modification time; a missing `Message-ID` becomes one derived from the message's content, so re-runs recognise the copy (F70; revises D40 for restore and migrate). |
| D185 | When the target refuses a folder or label name, or the name is one of the target's own system folders, restore and migrate write it as `<name> (restored)`, cut to the target's length limit, and log the rename once. Gmail's trimming of spaces in label names is followed silently (F71–F73). |
| D186 | One pimvault run at a time per state directory: a second `backup`, `verify`, `restore` or `migrate` exits 1 at once, naming the running process; a lock left by a crashed run is taken over. |
| D187 | Releases: tagged versions with static Linux binaries (amd64, arm64) built by a script with plain `go build`, published by the agent as GitHub Releases; `pimvault --version` prints the version. (refined by D194). |
| D188 | A folder or label name too long for a filename (255 bytes, once escaped) is stored in a directory named by the name cut at a character boundary plus `~` and 8 hex characters of a hash of the full name; the full name is written in a `.folder-name` file in that directory, which restore and migrate read. |
| D189 | `backup --dry-run` shows bytes for Gmail mail only, from Gmail's size estimate; for Outlook.com mail it says the size is unknown, as Outlook.com's stored size differs from the download by up to 15 times (F79; revises D183). |
| D190 | `verify` also reads every archive file: it parses `.ics` and `.vcf` files and checks that an `.eml` file is not empty and has a header. Files that fail are listed, counted as unreadable in the summary line, and make verify exit 1 (extends D81, D154). |
| D191 | `backup` backs up the selected accounts at the same time; within an account items still go one at a time (F83). Each account has its own Gmail quota (F80), archive directory, state file and sign-in. Summary lines print in the accounts' order, each once it and those before it are done. In a terminal the status line shows every account in progress (owner, 2026-11-01). |
| D192 | In Gmail, a message whose valid Message-ID holds an encoded-word counts as already there when Gmail has a message within a day of its Date from its sender with the same Message-ID (as written or decoded), Date and From. Gmail's Message-ID search cannot find such IDs (F84). Like D167, such a message without a valid Date is never found (owner, 2026-11-01). |
| D193 | The repository is public. The end-to-end test runs in GitHub Actions (`e2e.yml`), as the cloud environment cannot reach Outlook.com's IMAP port: on every push to `main` and when a cloud session dispatches it (`repository_dispatch` type `e2e`) for a branch before that branch's PR merges; never for `pull_request` or `pull_request_target`, so forks never get the secret. The service account key is the repository secret `PIMVAULT_E2E_SERVICE_ACCOUNT`; one run at a time; the test accounts' emails are masked in the public log. The result is the tested commit's `e2e` commit status (owner, 2026-11-22; replaces D157, revises D179). |
| D194 | Releases are published by the `release.yml` workflow, which a cloud session dispatches (`repository_dispatch` type `release` with the version): it checks `main`'s head, tags it, builds it with `script/release.sh` and uploads the binaries and `SHA256SUMS` with `GITHUB_TOKEN`. A version not higher than every existing tag is refused; running it again for an existing tag only finishes that release (owner, 2026-11-22; refines D187; revised by D198). |
| D195 | The weekly test sign-in (D159) runs in a cloud session: the agent starts `script/live/e2elogin` in the background, the owner opens the printed URL in a private tab on their phone, signs in as the test account and pastes the failed `127.0.0.1:53682/…` or `localhost:53682/…` address back; the tool stores the tokens in Secret Manager only if the signed-in account is the one the secret already holds (owner, 2026-11-22). |
| D196 | The repository was deleted and recreated from one commit before it went public, so personal email addresses that were in earlier history are not published; that history, its pull requests and the v0.1.0–v0.1.2 releases are gone. New versions stay above v0.1.2 (owner, 2026-11-23). |
| D197 | Releases are built at arm's length and can be pulled by anyone: the release workflow builds from the tag with no cache and no secret beyond `GITHUB_TOKEN`; builds are reproducible (exact Go version from `go.mod`, `-trimpath`); each release carries a Sigstore-signed GitHub build provenance attestation, also attached as `pimvault-vX.Y.Z.sigstore.json`. The repository knows nothing of who installs it: consumers poll the latest release and verify it themselves (owner, 2026-11-23). |
| D198 | Every merge to `main` publishes a release by itself, with no approval step or manual gate: when the CI run of the push to `main` succeeds, `release.yml` builds and tags that commit, if it is still `main`'s head (else the newer commit's run releases both), with the next patch version above every tag (and v0.1.2). Only `release.yml` run on `main` tags, releases or attests; a manual run is refused on any other ref, nothing runs on tag pushes, and cloud sessions never tag or release by hand. Consumers verify with `--signer-workflow .../release.yml --source-ref refs/heads/main` (owner, 2026-11-24; revises D194). |

## Open decisions

- O31. Decided by D166: meetings keep their attendees and are created with Graph `isDraft`, which sends nothing (F48, F50, F57); EWS `SendToNone` is not available to pimvault (F62).
- O138. Decided by D161 and built with D164: one browser sign-in consents to Graph and IMAP, and the refresh token is redeemed once per API (F62).
- O139. Decided by D180: the working assumption is kept as built.
